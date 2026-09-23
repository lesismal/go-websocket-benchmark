// The client side of RFC 6455: the upgrade request and its answer, masked frames out, and a
// parser for the server's frames in. Implemented here, as benchcli-go implements its own, rather
// than taken from a server library: what a benchmark client needs is a parser that holds nothing
// per connection but a partial frame - at a million connections a read buffer each is the
// difference between fitting and not - and one that reads from the event loop's shared buffer.
//
// The parser holds the server to what benchcli-uwscpp's uWS parser does: a frame longer than the
// payload being echoed (or than 125 bytes, whichever is more) closes the connection, as do a
// masked frame, a reserved bit (no extension is ever negotiated), a control frame that is
// fragmented or over 125 bytes, and a continuation that continues nothing. Fragmented messages
// are reassembled, with control frames allowed between their fragments.
use base64::Engine;
use sha1::{Digest, Sha1};

pub const OP_CONTINUATION: u8 = 0;
pub const OP_TEXT: u8 = 1;
pub const OP_BINARY: u8 = 2;
pub const OP_CLOSE: u8 = 8;
pub const OP_PING: u8 = 9;
pub const OP_PONG: u8 = 10;

const GUID: &str = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";

// The largest upgrade answer read before giving up on it, as benchcli-uwscpp has it.
pub const MAX_HANDSHAKE: usize = 16384;

// splitmix64: payloads, masks and handshake keys need to be unpredictable to nothing but a
// server's caching, so a fast generator seeded per thread is enough.
pub struct Rng(u64);

impl Rng {
    pub fn new() -> Rng {
        use std::hash::{BuildHasher, Hasher};
        let mut hasher = std::collections::hash_map::RandomState::new().build_hasher();
        hasher.write_u128(
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0),
        );
        Rng(hasher.finish())
    }

    pub fn next(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9E3779B97F4A7C15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
        z ^ (z >> 31)
    }

    pub fn fill(&mut self, out: &mut [u8]) {
        for chunk in out.chunks_mut(8) {
            let bytes = self.next().to_le_bytes();
            chunk.copy_from_slice(&bytes[..chunk.len()]);
        }
    }
}

// One masked frame from the client, FIN set.
pub fn client_frame(payload: &[u8], opcode: u8, rng: &mut Rng) -> Vec<u8> {
    let mut out = Vec::with_capacity(payload.len() + 14);
    out.push(0x80 | opcode);
    let len = payload.len();
    if len < 126 {
        out.push(0x80 | len as u8);
    } else if len < 65536 {
        out.push(0x80 | 126);
        out.extend_from_slice(&(len as u16).to_be_bytes());
    } else {
        out.push(0x80 | 127);
        out.extend_from_slice(&(len as u64).to_be_bytes());
    }
    let mut mask = [0u8; 4];
    rng.fill(&mut mask);
    out.extend_from_slice(&mask);
    out.extend(payload.iter().enumerate().map(|(i, b)| b ^ mask[i & 3]));
    out
}

pub fn handshake_key(rng: &mut Rng) -> String {
    let mut bytes = [0u8; 16];
    rng.fill(&mut bytes);
    base64::engine::general_purpose::STANDARD.encode(bytes)
}

pub fn accept_key(key: &str) -> String {
    let mut sha = Sha1::new();
    sha.update(key.as_bytes());
    sha.update(GUID.as_bytes());
    base64::engine::general_purpose::STANDARD.encode(sha.finalize())
}

pub fn upgrade_request(host: &str, port: u16, key: &str) -> Vec<u8> {
    format!(
        "GET /ws HTTP/1.1\r\nHost: {host}:{port}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\
         Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n"
    )
    .into_bytes()
}

// Whether an upgrade answer - its head, through the blank line - accepts the connection: a 101,
// the Sec-WebSocket-Accept the key calls for, Upgrade: websocket, Connection carrying the
// upgrade token, and no extension, since none was asked for. Header names and the two token
// values are compared without regard to case; repeated headers are joined with commas.
pub fn valid_upgrade(head: &[u8], accept: &str) -> bool {
    let Ok(head) = std::str::from_utf8(head) else {
        return false;
    };
    let mut lines = head.split("\r\n");
    let status = lines.next().unwrap_or("");
    if status != "HTTP/1.1 101" && !status.starts_with("HTTP/1.1 101 ") {
        return false;
    }
    let mut fields: std::collections::HashMap<String, String> = std::collections::HashMap::new();
    for line in lines {
        if line.is_empty() {
            break;
        }
        let Some((key, value)) = line.split_once(':') else {
            return false;
        };
        let key = key.to_ascii_lowercase();
        let value = value.trim_matches([' ', '\t']);
        fields
            .entry(key)
            .and_modify(|v| {
                v.push(',');
                v.push_str(value)
            })
            .or_insert_with(|| value.to_string());
    }
    let field = |k: &str| fields.get(k).map(String::as_str).unwrap_or("");
    field("sec-websocket-accept") == accept
        && field("upgrade").eq_ignore_ascii_case("websocket")
        && field("connection")
            .split(',')
            .any(|t| t.trim().eq_ignore_ascii_case("upgrade"))
        && !fields.contains_key("sec-websocket-extensions")
}

// What consume did.
#[derive(Debug, PartialEq, Eq)]
pub enum Outcome {
    // Everything was parsed, or kept for the next read.
    Done,
    // The handler asked to stop: the connection it was delivering to is gone.
    Stopped,
    // The server broke the protocol, or the limits; the caller closes the connection.
    Violation,
}

#[derive(Default)]
pub struct Parser {
    // A frame not yet read whole, from an earlier read.
    pending: Vec<u8>,
    // The fragments of a message so far, and its opcode.
    message: Vec<u8>,
    message_op: Option<u8>,
}

impl Parser {
    // Parses data, delivering each whole message - and each control frame, fragments or not
    // around it - to handler as (opcode, payload), and keeping a partial frame for the next
    // call. handler returns false to stop. max_payload is the most a data message may carry.
    pub fn consume(
        &mut self,
        data: &[u8],
        max_payload: usize,
        handler: &mut dyn FnMut(u8, &[u8]) -> bool,
    ) -> Outcome {
        let max_payload = max_payload.max(125);
        if self.pending.is_empty() {
            let (outcome, used) = self.parse(data, max_payload, handler);
            if outcome == Outcome::Done {
                self.pending.extend_from_slice(&data[used..]);
            }
            return outcome;
        }
        let mut buffer = std::mem::take(&mut self.pending);
        buffer.extend_from_slice(data);
        let (outcome, used) = self.parse(&buffer, max_payload, handler);
        if outcome == Outcome::Done {
            buffer.drain(..used);
            self.pending = buffer;
        }
        outcome
    }

    fn parse(
        &mut self,
        data: &[u8],
        max_payload: usize,
        handler: &mut dyn FnMut(u8, &[u8]) -> bool,
    ) -> (Outcome, usize) {
        let mut at = 0;
        loop {
            let rest = &data[at..];
            if rest.len() < 2 {
                return (Outcome::Done, at);
            }
            let (first, second) = (rest[0], rest[1]);
            let fin = first & 0x80 != 0;
            let opcode = first & 0x0F;
            if first & 0x70 != 0 || second & 0x80 != 0 {
                return (Outcome::Violation, at);
            }
            let (len, header) = match second & 0x7F {
                126 if rest.len() >= 4 => (u16::from_be_bytes([rest[2], rest[3]]) as u64, 4),
                127 if rest.len() >= 10 => {
                    (u64::from_be_bytes(rest[2..10].try_into().unwrap()), 10)
                }
                126 | 127 => return (Outcome::Done, at),
                n => (n as u64, 2),
            };
            let control = opcode >= 8;
            if control && (!fin || len > 125) {
                return (Outcome::Violation, at);
            }
            if !matches!(
                opcode,
                OP_CONTINUATION | OP_TEXT | OP_BINARY | OP_CLOSE | OP_PING | OP_PONG
            ) {
                return (Outcome::Violation, at);
            }
            if len > max_payload as u64 {
                return (Outcome::Violation, at);
            }
            let end = header + len as usize;
            if rest.len() < end {
                return (Outcome::Done, at);
            }
            let payload = &rest[header..end];
            at += end;

            let keep_going = if control {
                handler(opcode, payload)
            } else if opcode == OP_CONTINUATION {
                let Some(op) = self.message_op else {
                    return (Outcome::Violation, at);
                };
                if self.message.len() + payload.len() > max_payload {
                    return (Outcome::Violation, at);
                }
                self.message.extend_from_slice(payload);
                if fin {
                    self.message_op = None;
                    let message = std::mem::take(&mut self.message);
                    let keep_going = handler(op, &message);
                    // Keep the allocation for the next fragmented message.
                    self.message = message;
                    self.message.clear();
                    keep_going
                } else {
                    true
                }
            } else {
                if self.message_op.is_some() {
                    return (Outcome::Violation, at);
                }
                if fin {
                    handler(opcode, payload)
                } else {
                    self.message_op = Some(opcode);
                    self.message.extend_from_slice(payload);
                    true
                }
            };
            if !keep_going {
                return (Outcome::Stopped, at);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn server_frame(payload: &[u8], opcode: u8, fin: bool) -> Vec<u8> {
        let mut out = vec![(if fin { 0x80 } else { 0 }) | opcode];
        if payload.len() < 126 {
            out.push(payload.len() as u8);
        } else if payload.len() < 65536 {
            out.push(126);
            out.extend_from_slice(&(payload.len() as u16).to_be_bytes());
        } else {
            out.push(127);
            out.extend_from_slice(&(payload.len() as u64).to_be_bytes());
        }
        out.extend_from_slice(payload);
        out
    }

    fn collect(parser: &mut Parser, data: &[u8], max: usize) -> (Outcome, Vec<(u8, Vec<u8>)>) {
        let mut got = Vec::new();
        let outcome = parser.consume(data, max, &mut |op, p| {
            got.push((op, p.to_vec()));
            true
        });
        (outcome, got)
    }

    #[test]
    fn whole_split_and_fragmented_messages() {
        let payload: Vec<u8> = (0..70000u32).map(|i| i as u8).collect();
        for size in [1usize, 125, 126, 65535, 65536, 70000] {
            let frame = server_frame(&payload[..size], OP_BINARY, true);
            // One byte at a time, so every header and length form is split.
            let mut parser = Parser::default();
            let mut got = Vec::new();
            for b in &frame {
                let (outcome, mut part) = collect(&mut parser, std::slice::from_ref(b), size);
                assert_eq!(outcome, Outcome::Done);
                got.append(&mut part);
            }
            assert_eq!(got, vec![(OP_BINARY, payload[..size].to_vec())]);
        }

        let mut data = server_frame(b"hel", OP_BINARY, false);
        data.extend(server_frame(b"ping", OP_PING, true));
        data.extend(server_frame(b"lo", OP_CONTINUATION, true));
        let (outcome, got) = collect(&mut Parser::default(), &data, 5);
        assert_eq!(outcome, Outcome::Done);
        assert_eq!(
            got,
            vec![(OP_PING, b"ping".to_vec()), (OP_BINARY, b"hello".to_vec())]
        );
    }

    #[test]
    fn violations() {
        let too_long = server_frame(&[0; 200], OP_BINARY, true);
        assert_eq!(
            collect(&mut Parser::default(), &too_long, 128).0,
            Outcome::Violation
        );
        let mut masked = server_frame(b"x", OP_BINARY, true);
        masked[1] |= 0x80;
        assert_eq!(
            collect(&mut Parser::default(), &masked, 128).0,
            Outcome::Violation
        );
        let mut rsv = server_frame(b"x", OP_BINARY, true);
        rsv[0] |= 0x40;
        assert_eq!(
            collect(&mut Parser::default(), &rsv, 128).0,
            Outcome::Violation
        );
        let stray = server_frame(b"x", OP_CONTINUATION, true);
        assert_eq!(
            collect(&mut Parser::default(), &stray, 128).0,
            Outcome::Violation
        );
        let fragmented_ping = server_frame(b"x", OP_PING, false);
        assert_eq!(
            collect(&mut Parser::default(), &fragmented_ping, 128).0,
            Outcome::Violation
        );
    }

    #[test]
    fn client_frames_are_masked() {
        let mut rng = Rng::new();
        let frame = client_frame(b"abc", OP_BINARY, &mut rng);
        assert_eq!(frame[0], 0x82);
        assert_eq!(frame[1], 0x83);
        let mask = &frame[2..6];
        let payload: Vec<u8> = frame[6..]
            .iter()
            .enumerate()
            .map(|(i, b)| b ^ mask[i & 3])
            .collect();
        assert_eq!(payload, b"abc");
    }

    #[test]
    fn upgrade_answers() {
        let key = "dGhlIHNhbXBsZSBub25jZQ==";
        let accept = accept_key(key);
        assert_eq!(accept, "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=");
        let good = format!(
            "HTTP/1.1 101 Switching Protocols\r\nUpgrade: WebSocket\r\nConnection: keep-alive, Upgrade\r\nSec-WebSocket-Accept: {accept}\r\n\r\n"
        );
        assert!(valid_upgrade(good.as_bytes(), &accept));
        assert!(!valid_upgrade(
            good.replace(&accept, "bad").as_bytes(),
            &accept
        ));
        assert!(!valid_upgrade(
            good.replace("101", "200").as_bytes(),
            &accept
        ));
        let extension = good.replace("\r\n\r\n", "\r\nSec-WebSocket-Extensions: x\r\n\r\n");
        assert!(!valid_upgrade(extension.as_bytes(), &accept));
    }
}
