// The socket under every connection's WebSocketStream: a TcpStream whose reads go through one
// large buffer per loop thread, the way uSockets reads through one receive buffer per loop, so
// that a connection holds no read buffer of its own while it has nothing to read.
//
// tungstenite keeps a read buffer per connection (read_buffer_size, 128KiB by default) and
// zero-fills the part it reads into, up to that size, before every read - so every connection
// that has read once has all of it resident, and every read pays a memset of it. At 50000
// connections that was 6.85G for this server in BenchEcho. So read_buffer_size is 4KiB here, the
// size tungstenite's documentation suggests for many connections (see ws_config in main.rs),
// and a read takes everything the socket has, up to SCRATCH, in one recv; tungstenite gets its
// 4KiB at a time from memory. What it has not taken yet waits in the connection's stash, which
// comes from a per-thread pool and goes back as soon as it is empty - almost always before the
// connection's task yields, since tungstenite reads until it has no whole frame left.
//
// Writes the same way. tungstenite gathers a batch's frames in a write buffer of its own that
// never shrinks, so every connection that had echoed a burst kept its peak: BenchRate went from
// 6.85G to 8.8G at 50000 connections, and still held 1.8-2.6G with the reads fixed. So
// write_buffer_size is 0 (see ws_config), which has tungstenite hand each frame here as it is
// framed, and here the frames gather in a pooled buffer that the flush the echo loop makes after
// each batch writes out in one send and hands back. A connection holds a write buffer only while
// its peer is not taking what it was sent, and never more than CORK past what was written.
use std::cell::RefCell;
use std::io;
use std::pin::Pin;
use std::task::{Context, Poll};

use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};
use tokio::net::TcpStream;

// The most one recv takes: uSockets' LIBUS_RECV_BUFFER_LENGTH.
const SCRATCH: usize = 512 * 1024;
// Stashes kept for reuse per thread, and the largest capacity worth keeping.
const POOLED: usize = 64;
const POOLED_CAPACITY: usize = 64 * 1024;
// Unwritten bytes past which a write goes to the socket before it gathers more, as tungstenite's
// write_buffer_size does, so that a connection whose peer stopped reading stops the echo loop
// rather than growing without bound.
const CORK: usize = 64 * 1024;

thread_local! {
    static SCRATCH_BUF: RefCell<Box<[u8]>> = RefCell::new(vec![0; SCRATCH].into_boxed_slice());
    static POOL: RefCell<Vec<Vec<u8>>> = const { RefCell::new(Vec::new()) };
}

pub struct Stream {
    inner: TcpStream,
    // Read from the socket but not yet handed to tungstenite, from `pos` on.
    stash: Vec<u8>,
    pos: usize,
    // Written by tungstenite but not yet to the socket, from `sent` on.
    out: Vec<u8>,
    sent: usize,
}

impl Stream {
    pub fn new(inner: TcpStream) -> Stream {
        Stream {
            inner,
            stash: Vec::new(),
            pos: 0,
            out: Vec::new(),
            sent: 0,
        }
    }

    // Writes what has gathered, as far as the socket takes it; Ready once it is all out, when
    // the buffer goes back to the pool.
    fn poll_drain(&mut self, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        while self.sent < self.out.len() {
            match Pin::new(&mut self.inner).poll_write(cx, &self.out[self.sent..]) {
                Poll::Ready(Ok(0)) => return Poll::Ready(Err(io::ErrorKind::WriteZero.into())),
                Poll::Ready(Ok(n)) => self.sent += n,
                Poll::Ready(Err(e)) => return Poll::Ready(Err(e)),
                Poll::Pending => return Poll::Pending,
            }
        }
        release(std::mem::take(&mut self.out));
        self.sent = 0;
        Poll::Ready(Ok(()))
    }
}

// Takes a buffer from this thread's pool, or a new one.
fn pooled() -> Vec<u8> {
    POOL.with(|pool| pool.borrow_mut().pop())
        .unwrap_or_default()
}

// Hands a buffer back to this thread's pool, unless it is empty or grew too large to keep.
fn release(mut buf: Vec<u8>) {
    if buf.capacity() == 0 || buf.capacity() > POOLED_CAPACITY {
        return;
    }
    buf.clear();
    POOL.with(|pool| {
        let mut pool = pool.borrow_mut();
        if pool.len() < POOLED {
            pool.push(buf);
        }
    });
}

impl Drop for Stream {
    fn drop(&mut self) {
        // tungstenite writes the last of a close handshake and ends the connection without a
        // flush, so what has gathered goes out now, as far as the socket takes it without
        // waiting - what a write straight to the socket would have managed.
        if self.sent < self.out.len() {
            let _ = self.inner.try_write(&self.out[self.sent..]);
        }
        release(std::mem::take(&mut self.stash));
        release(std::mem::take(&mut self.out));
    }
}

impl AsyncRead for Stream {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        if this.pos < this.stash.len() {
            let n = buf.remaining().min(this.stash.len() - this.pos);
            buf.put_slice(&this.stash[this.pos..this.pos + n]);
            this.pos += n;
            if this.pos == this.stash.len() {
                release(std::mem::take(&mut this.stash));
                this.pos = 0;
            }
            return Poll::Ready(Ok(()));
        }
        SCRATCH_BUF.with(|scratch| {
            let mut scratch = scratch.borrow_mut();
            let mut read = ReadBuf::new(&mut scratch[..]);
            match Pin::new(&mut this.inner).poll_read(cx, &mut read) {
                Poll::Ready(Ok(())) => {}
                other => return other,
            }
            let data = read.filled();
            let n = buf.remaining().min(data.len());
            buf.put_slice(&data[..n]);
            if n < data.len() {
                let mut stash = pooled();
                stash.extend_from_slice(&data[n..]);
                this.stash = stash;
                this.pos = 0;
            }
            Poll::Ready(Ok(()))
        })
    }
}

// Writes gather until a flush, or until CORK is unwritten.
impl AsyncWrite for Stream {
    fn poll_write(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        let this = self.get_mut();
        if this.out.len() - this.sent >= CORK {
            std::task::ready!(this.poll_drain(cx))?;
        }
        if this.out.capacity() == 0 {
            this.out = pooled();
        }
        this.out.extend_from_slice(buf);
        Poll::Ready(Ok(buf.len()))
    }

    fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        std::task::ready!(this.poll_drain(cx))?;
        Pin::new(&mut this.inner).poll_flush(cx)
    }

    fn poll_shutdown(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        std::task::ready!(this.poll_drain(cx))?;
        Pin::new(&mut this.inner).poll_shutdown(cx)
    }
}
