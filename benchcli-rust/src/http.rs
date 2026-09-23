// The control requests - /init, /ps, /taskpool and the pprof routes - over a plain HTTP/1.1
// connection each, the way benchcli-uwscpp sends them with libcurl: Connection: close, a connect
// timeout and an overall one, and a status other than 2xx as an error. Bodies come with a
// Content-Length, chunked (the Go servers' pprof routes), or up to the close.
use std::io::{Read, Write};
use std::net::{TcpStream, ToSocketAddrs};
use std::time::{Duration, Instant};

use crate::metadata::ports;
use crate::options::Options;

pub enum HttpError {
    // The server answered, with a status that is not a success: a missing route, most likely.
    // Waiting will not put it there, so this is not retried.
    Status(String),
    // The request did not get an answer at all.
    Transport(String),
}

impl std::fmt::Display for HttpError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            HttpError::Status(s) | HttpError::Transport(s) => f.write_str(s),
        }
    }
}

pub fn http(url: &str, body: Option<&str>, timeout: Duration) -> Result<Vec<u8>, HttpError> {
    let fail = |msg: String| HttpError::Transport(format!("{url}: {msg}"));
    let rest = url
        .strip_prefix("http://")
        .ok_or_else(|| fail("not an http:// URL".into()))?;
    let (authority, path) = match rest.find('/') {
        Some(i) => (&rest[..i], &rest[i..]),
        None => (rest, "/"),
    };
    let deadline = Instant::now() + timeout;
    let addrs: Vec<_> = authority
        .to_socket_addrs()
        .map_err(|e| fail(format!("cannot resolve host: {e}")))?
        .collect();
    let mut stream = None;
    let mut last = String::from("no address");
    for addr in addrs {
        match TcpStream::connect_timeout(&addr, Duration::from_secs(5).min(timeout)) {
            Ok(s) => {
                stream = Some(s);
                break;
            }
            Err(e) => last = format!("couldn't connect to server: {e}"),
        }
    }
    let mut stream = stream.ok_or_else(|| fail(last))?;
    let request = match body {
        Some(body) => format!(
            "POST {path} HTTP/1.1\r\nHost: {authority}\r\nContent-Type: application/x-www-form-urlencoded\r\n\
             Content-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        ),
        None => format!("GET {path} HTTP/1.1\r\nHost: {authority}\r\nConnection: close\r\n\r\n"),
    };
    stream
        .write_all(request.as_bytes())
        .map_err(|e| fail(format!("send failed: {e}")))?;

    let mut data = Vec::new();
    let mut buf = [0u8; 16384];
    loop {
        let left = deadline.saturating_duration_since(Instant::now());
        if left.is_zero() {
            return Err(fail("timed out".into()));
        }
        stream.set_read_timeout(Some(left)).ok();
        match stream.read(&mut buf) {
            Ok(0) => break,
            Ok(n) => data.extend_from_slice(&buf[..n]),
            Err(e) if e.kind() == std::io::ErrorKind::Interrupted => continue,
            Err(e)
                if matches!(
                    e.kind(),
                    std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut
                ) =>
            {
                return Err(fail("timed out".into()));
            }
            Err(e) => {
                // A reset after a complete answer is still an answer.
                if parse_response(&data).is_some() {
                    break;
                }
                return Err(fail(format!("receive failed: {e}")));
            }
        }
        if let Some(Some(_)) = parse_response(&data).map(|r| r.complete) {
            break;
        }
    }
    let response = parse_response(&data).ok_or_else(|| fail("malformed response".into()))?;
    if !(200..300).contains(&response.status) {
        return Err(HttpError::Status(format!(
            "{url}: HTTP response code said error: {}",
            response.status
        )));
    }
    Ok(response.complete.unwrap_or(response.body_so_far))
}

struct Response {
    status: u16,
    // The body, once all of it is here as far as its framing says.
    complete: Option<Vec<u8>>,
    // What there is of it: the body of a response that ends at the close.
    body_so_far: Vec<u8>,
}

fn parse_response(data: &[u8]) -> Option<Response> {
    let head_end = data.windows(4).position(|w| w == b"\r\n\r\n")?;
    let head = std::str::from_utf8(&data[..head_end]).ok()?;
    let mut lines = head.split("\r\n");
    let status: u16 = lines.next()?.split_whitespace().nth(1)?.parse().ok()?;
    let mut length = None;
    let mut chunked = false;
    for line in lines {
        let (k, v) = line.split_once(':')?;
        if k.eq_ignore_ascii_case("content-length") {
            length = v.trim().parse::<usize>().ok();
        } else if k.eq_ignore_ascii_case("transfer-encoding")
            && v.to_ascii_lowercase().contains("chunked")
        {
            chunked = true;
        }
    }
    let body = &data[head_end + 4..];
    let complete = if chunked {
        dechunk(body)
    } else if let Some(n) = length {
        (body.len() >= n).then(|| body[..n].to_vec())
    } else {
        None
    };
    Some(Response {
        status,
        complete,
        body_so_far: body.to_vec(),
    })
}

fn dechunk(mut body: &[u8]) -> Option<Vec<u8>> {
    let mut out = Vec::new();
    loop {
        let line_end = body.windows(2).position(|w| w == b"\r\n")?;
        let size_text = std::str::from_utf8(&body[..line_end]).ok()?;
        let size = usize::from_str_radix(size_text.split(';').next()?.trim(), 16).ok()?;
        body = &body[line_end + 2..];
        if size == 0 {
            return Some(out);
        }
        if body.len() < size + 2 {
            return None;
        }
        out.extend_from_slice(&body[..size]);
        body = &body[size + 2..];
    }
}

// Control requests go to the pid port, which for most frameworks is also carrying benchmark
// connections. At a hundred thousand of them one attempt is not enough: a server still working
// through the backlog of a just-finished rate test can reset the connection or sit on the
// request past any deadline, and the resource columns that silently read 0 when that happened
// took EER down with them. So a transport failure is retried, patiently; a reply the server
// produced, 404 included, is returned as it is.
pub const CONTROL_ATTEMPTS: u32 = 4;
const CONTROL_TIMEOUT: Duration = Duration::from_secs(30);

pub fn http_retry(url: &str, body: Option<&str>, attempts: u32) -> Result<Vec<u8>, String> {
    let mut last = String::new();
    for attempt in 1..=attempts {
        if attempt > 1 {
            std::thread::sleep(Duration::from_secs(2 * (attempt as u64 - 1)));
        }
        match http(url, body, CONTROL_TIMEOUT) {
            Ok(data) => return Ok(data),
            Err(HttpError::Status(e)) => return Err(e),
            Err(HttpError::Transport(e)) => {
                if attempt < attempts {
                    eprintln!("control request failed, retrying ({attempt}/{attempts}): {e}");
                }
                last = e;
            }
        }
    }
    Err(last)
}

// The base URL of a framework's control routes: the last of its benchmark ports, or the one
// after it for the four frameworks that serve their control routes separately. Mirrors
// config.FrameworkControlAddr.
pub fn control_url(o: &Options) -> String {
    let mut host = o.get("ip").to_string();
    if host.contains(':') && !host.starts_with('[') {
        host = format!("[{host}]");
    }
    let f = o.get("f");
    let mut port = ports(f).1;
    if matches!(f, "fib" | "gws" | "uws_events" | "uws_std") {
        port += 1;
    }
    format!("http://{host}:{port}")
}

// The pool the server installed, for the report's Pool: "-" for a server that installs none,
// the frameworks that take no -taskpool flag and any whose /taskpool route does not answer.
// Mirrors config.GetFrameworkTaskPool.
pub fn framework_task_pool(o: &Options) -> String {
    match http_retry(&format!("{}/taskpool", control_url(o)), None, 2) {
        Ok(body) => {
            let name = String::from_utf8_lossy(&body).trim().to_string();
            if name.is_empty() { "-".into() } else { name }
        }
        Err(_) => "-".into(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn responses() {
        let r = parse_response(b"HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\n123").unwrap();
        assert_eq!((r.status, r.complete.as_deref()), (200, Some(&b"123"[..])));
        let r = parse_response(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n2\r\nde\r\n0\r\n\r\n").unwrap();
        assert_eq!(r.complete.as_deref(), Some(&b"abcde"[..]));
        let r = parse_response(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nab")
            .unwrap();
        assert_eq!(r.complete, None);
        let r = parse_response(b"HTTP/1.0 404 Not Found\r\n\r\nnope").unwrap();
        assert_eq!(
            (r.status, r.complete, r.body_so_far),
            (404, None, b"nope".to_vec())
        );
    }
}
