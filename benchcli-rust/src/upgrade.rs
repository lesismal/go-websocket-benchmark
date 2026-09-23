// The upgrade: the request, and whether the server's answer accepts it. Everything after it is
// tokio-tungstenite's, from WebSocketStream::from_partially_read, and so are the key and the
// accept value it is checked against (tungstenite's generate_key and derive_accept_key).
//
// The answer is checked here rather than by tungstenite's client handshake because that one
// takes Connection for a single value: it fails a server that answers "Connection: keep-alive,
// Upgrade", which RFC 6455 allows - the header need only carry the upgrade token - and a
// benchmark client that counts a conforming server's connections as failed is measuring itself.
// The check is benchcli-uwscpp's: a 101, the Sec-WebSocket-Accept the key calls for, Upgrade:
// websocket, Connection carrying the upgrade token, and no extension, since none was asked for.

// The largest upgrade answer read before giving up on it, as benchcli-uwscpp has it.
pub const MAX_HANDSHAKE: usize = 16384;

pub fn request(host: &str, port: u16, key: &str) -> String {
    format!(
        "GET /ws HTTP/1.1\r\nHost: {host}:{port}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\
         Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n"
    )
}

// Header names and the two token values are compared without regard to case; repeated headers
// are joined with commas.
pub fn accepted(head: &[u8], accept: &str) -> bool {
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
        let value = value.trim_matches([' ', '\t']);
        fields
            .entry(key.to_ascii_lowercase())
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

#[cfg(test)]
mod tests {
    use super::accepted;
    use tokio_tungstenite::tungstenite::handshake::derive_accept_key;

    #[test]
    fn answers() {
        let accept = derive_accept_key(b"dGhlIHNhbXBsZSBub25jZQ==");
        assert_eq!(accept, "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=");
        let good = format!(
            "HTTP/1.1 101 Switching Protocols\r\nUpgrade: WebSocket\r\nConnection: keep-alive, Upgrade\r\n\
             Sec-WebSocket-Accept: {accept}\r\n\r\n"
        );
        assert!(accepted(good.as_bytes(), &accept));
        assert!(!accepted(good.replace(&accept, "bad").as_bytes(), &accept));
        assert!(!accepted(good.replace("101", "200").as_bytes(), &accept));
        assert!(!accepted(
            good.replace("keep-alive, Upgrade", "keep-alive").as_bytes(),
            &accept
        ));
        let extension = good.replace("\r\n\r\n", "\r\nSec-WebSocket-Extensions: x\r\n\r\n");
        assert!(!accepted(extension.as_bytes(), &accept));
    }
}
