use std::env;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::process;

use native_tls::TlsConnector;
use serde::Deserialize;

const USAGE: &str = "\
gmail-inbox: run this app under Fabricate.

  fab run acme-gmail -- ./target/debug/gmail-inbox
  fab run acme-gmail --proxy -- ./target/debug/gmail-inbox

Optional search: ./target/debug/gmail-inbox INV-4812
";

#[derive(Debug, Deserialize)]
struct Profile {
    #[serde(rename = "emailAddress")]
    email_address: Option<String>,
    #[serde(rename = "messagesTotal")]
    messages_total: Option<i64>,
}

#[derive(Debug, Deserialize)]
struct MessageList {
    messages: Option<Vec<MessageRef>>,
    #[serde(rename = "resultSizeEstimate")]
    result_size_estimate: Option<i64>,
}

#[derive(Debug, Deserialize)]
struct MessageRef {
    id: String,
}

#[derive(Debug, Deserialize)]
struct Message {
    snippet: Option<String>,
    payload: Option<Payload>,
}

#[derive(Debug, Deserialize)]
struct Payload {
    headers: Option<Vec<Header>>,
}

#[derive(Debug, Deserialize)]
struct Header {
    name: Option<String>,
    value: Option<String>,
}

fn env_first(names: &[&str]) -> Option<String> {
    names.iter().find_map(|name| env::var(name).ok().filter(|value| !value.is_empty()))
}

fn using_proxy() -> bool {
    env_first(&["HTTPS_PROXY", "https_proxy"]).is_some()
}

fn gmail_base() -> (String, Option<String>) {
    if using_proxy() {
        return ("https://gmail.googleapis.com/gmail/v1".to_string(), None);
    }
    let url = env_first(&["FAB_SUPPORT_MAIL_URL", "FAB_GMAIL_URL"]);
    let token = env_first(&["FAB_SUPPORT_MAIL_TOKEN", "FAB_GMAIL_TOKEN"]);
    match (url, token) {
        (Some(url), Some(token)) => (format!("{}/gmail/v1", url.trim_end_matches('/')), Some(token)),
        _ => {
            eprint!("{USAGE}");
            process::exit(1);
        }
    }
}

fn tls_connector() -> TlsConnector {
    let mut builder = TlsConnector::builder();
    if let Some(path) = env_first(&["SSL_CERT_FILE", "REQUESTS_CA_BUNDLE"]) {
        let pem = std::fs::read(&path).unwrap_or_else(|err| {
            eprintln!("gmail-inbox: read CA {path}: {err}");
            process::exit(1);
        });
        let cert = native_tls::Certificate::from_pem(&pem).unwrap_or_else(|err| {
            eprintln!("gmail-inbox: parse CA {path}: {err}");
            process::exit(1);
        });
        builder.add_root_certificate(cert);
    }
    builder.build().unwrap_or_else(|err| {
        eprintln!("gmail-inbox: tls: {err}");
        process::exit(1);
    })
}

fn parse_url(url: &str) -> (bool, String, u16, String) {
    let https = if let Some(rest) = url.strip_prefix("https://") {
        (true, rest)
    } else if let Some(rest) = url.strip_prefix("http://") {
        (false, rest)
    } else {
        eprintln!("gmail-inbox: unsupported URL {url}");
        process::exit(1);
    };
    let (hostport, path) = https.1.split_once('/').unwrap_or((https.1, ""));
    let path = format!("/{path}");
    let (host, port) = if let Some((host, port)) = hostport.split_once(':') {
        (host.to_string(), port.parse().unwrap_or(if https.0 { 443 } else { 80 }))
    } else {
        (hostport.to_string(), if https.0 { 443 } else { 80 })
    };
    (https.0, host, port, path)
}

fn parse_proxy(proxy: &str) -> (String, u16) {
    let rest = proxy
        .strip_prefix("http://")
        .or_else(|| proxy.strip_prefix("https://"))
        .unwrap_or(proxy);
    let rest = rest.trim_end_matches('/');
    if let Some((host, port)) = rest.split_once(':') {
        (host.to_string(), port.parse().unwrap_or(8080))
    } else {
        (rest.to_string(), 8080)
    }
}

fn read_headers(stream: &mut impl Read) -> String {
    let mut buf = Vec::new();
    let mut byte = [0u8; 1];
    loop {
        let n = stream.read(&mut byte).unwrap_or_else(|err| {
            eprintln!("gmail-inbox: read headers: {err}");
            process::exit(1);
        });
        if n == 0 {
            break;
        }
        buf.push(byte[0]);
        if buf.ends_with(b"\r\n\r\n") {
            break;
        }
        if buf.len() > 16 * 1024 {
            eprintln!("gmail-inbox: HTTP headers too large");
            process::exit(1);
        }
    }
    String::from_utf8_lossy(&buf).into_owned()
}

fn read_http_response(stream: &mut impl Read) -> (u16, String) {
    let mut buf = Vec::new();
    stream.read_to_end(&mut buf).unwrap_or_else(|err| {
        eprintln!("gmail-inbox: read: {err}");
        process::exit(1);
    });
    let raw = String::from_utf8_lossy(&buf);
    let (head, body) = raw.split_once("\r\n\r\n").unwrap_or((raw.as_ref(), ""));
    let status = head
        .split_whitespace()
        .nth(1)
        .and_then(|s| s.parse().ok())
        .unwrap_or(0);
    (status, body.to_string())
}

fn get_json<T: for<'de> Deserialize<'de>>(url: &str, token: Option<&str>) -> T {
    let (https, host, port, path) = parse_url(url);
    let mut request = format!("GET {path} HTTP/1.1\r\nHost: {host}\r\nAccept: application/json\r\nConnection: close\r\n");
    if let Some(token) = token {
        request.push_str(&format!("Authorization: Bearer {token}\r\n"));
    }
    request.push_str("\r\n");

    let body = if https {
        let connector = tls_connector();
        let proxy = env_first(&["HTTPS_PROXY", "https_proxy"]);
        let mut tls_stream = if let Some(proxy) = proxy {
            let (proxy_host, proxy_port) = parse_proxy(&proxy);
            let mut stream = TcpStream::connect((proxy_host.as_str(), proxy_port)).unwrap_or_else(|err| {
                eprintln!("gmail-inbox: proxy {proxy_host}:{proxy_port}: {err}");
                process::exit(1);
            });
            let connect = format!("CONNECT {host}:{port} HTTP/1.1\r\nHost: {host}:{port}\r\n\r\n");
            stream.write_all(connect.as_bytes()).unwrap();
            let preview = read_headers(&mut stream);
            if !preview.starts_with("HTTP/1.1 200") && !preview.starts_with("HTTP/1.0 200") {
                eprintln!("gmail-inbox: proxy CONNECT failed\n{preview}");
                process::exit(1);
            }
            connector.connect(&host, stream).unwrap_or_else(|err| {
                eprintln!("gmail-inbox: tls handshake {host}: {err}");
                process::exit(1);
            })
        } else {
            let stream = TcpStream::connect((host.as_str(), port)).unwrap_or_else(|err| {
                eprintln!("gmail-inbox: connect {host}:{port}: {err}");
                process::exit(1);
            });
            connector.connect(&host, stream).unwrap_or_else(|err| {
                eprintln!("gmail-inbox: tls handshake {host}: {err}");
                process::exit(1);
            })
        };
        tls_stream.write_all(request.as_bytes()).unwrap();
        let (status, body) = read_http_response(&mut tls_stream);
        if !(200..300).contains(&status) {
            eprintln!("gmail-inbox: {status} {url}\n{body}");
            process::exit(1);
        }
        body
    } else {
        let mut stream = TcpStream::connect((host.as_str(), port)).unwrap_or_else(|err| {
            eprintln!("gmail-inbox: connect {host}:{port}: {err}");
            process::exit(1);
        });
        stream.write_all(request.as_bytes()).unwrap();
        let (status, body) = read_http_response(&mut stream);
        if !(200..300).contains(&status) {
            eprintln!("gmail-inbox: {status} {url}\n{body}");
            process::exit(1);
        }
        body
    };

    serde_json::from_str(&body).unwrap_or_else(|err| {
        eprintln!("gmail-inbox: decode {url}: {err}\n{body}");
        process::exit(1);
    })
}

fn header<'a>(message: &'a Message, name: &str) -> &'a str {
    let Some(payload) = message.payload.as_ref() else {
        return "";
    };
    let Some(headers) = payload.headers.as_ref() else {
        return "";
    };
    headers
        .iter()
        .find(|item| item.name.as_deref().is_some_and(|n| n.eq_ignore_ascii_case(name)))
        .and_then(|item| item.value.as_deref())
        .unwrap_or("")
}

fn main() {
    let (base, token) = gmail_base();
    let query = env::args().skip(1).collect::<Vec<_>>().join(" ");
    let query = query.trim();
    let token = token.as_deref();

    let profile: Profile = get_json(&format!("{base}/users/me/profile"), token);
    let mut list_url = format!("{base}/users/me/messages?maxResults=8");
    if !query.is_empty() {
        list_url.push_str("&q=");
        list_url.push_str(&urlencoding_minimal(query));
    }
    let listed: MessageList = get_json(&list_url, token);

    let email = profile.email_address.as_deref().unwrap_or("unknown");
    let total = profile.messages_total.map(|n| n.to_string()).unwrap_or_else(|| "?".into());
    let estimate = listed.result_size_estimate.unwrap_or(0);
    let mut title = format!("{email}  ·  {total} messages");
    if !query.is_empty() {
        title.push_str(&format!("  ·  search {query:?} ({estimate})"));
    }
    println!("{title}\n");

    let messages = listed.messages.unwrap_or_default();
    if messages.is_empty() {
        println!("  (no messages)");
        return;
    }
    for item in messages {
        let message: Message = get_json(&format!("{base}/users/me/messages/{}", item.id), token);
        let subject = header(&message, "Subject");
        let subject = if subject.is_empty() { "(no subject)" } else { subject };
        let sender = header(&message, "From");
        let sender = if sender.is_empty() { "(unknown sender)" } else { sender };
        println!("  {subject}");
        println!("    {sender}");
        if let Some(snippet) = message.snippet.as_deref().map(str::trim).filter(|s| !s.is_empty()) {
            println!("    {snippet}");
        }
        println!();
    }
}

fn urlencoding_minimal(value: &str) -> String {
    let mut out = String::new();
    for byte in value.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => out.push(byte as char),
            b' ' => out.push('+'),
            _ => out.push_str(&format!("%{byte:02X}")),
        }
    }
    out
}
