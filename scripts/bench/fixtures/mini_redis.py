# Checks the mini-redis DEL and EXISTS task with tests the model never sees.
#
#   python3 mini_redis.py check

import os
import socket
import subprocess
import sys
import time
from pathlib import Path

TEST = r"""
use mini_redis::clients::{BlockingClient, Client};
use mini_redis::server;
use std::net::SocketAddr;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

async fn start() -> SocketAddr {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { server::run(listener, tokio::signal::ctrl_c()).await });
    addr
}

async fn reply(stream: &mut TcpStream, request: &[u8], expected: &[u8]) {
    stream.write_all(request).await.unwrap();
    let mut response = vec![0; expected.len()];
    stream.read_exact(&mut response).await.unwrap();
    assert_eq!(String::from_utf8_lossy(expected), String::from_utf8_lossy(&response));
}

#[tokio::test]
async fn hidden_protocol() {
    let addr = start().await;
    let mut stream = TcpStream::connect(addr).await.unwrap();
    reply(&mut stream, b"*3\r\n$3\r\nSET\r\n$1\r\na\r\n$1\r\n1\r\n", b"+OK\r\n").await;
    reply(&mut stream, b"*3\r\n$3\r\nSET\r\n$1\r\nb\r\n$1\r\n2\r\n", b"+OK\r\n").await;
    reply(&mut stream, b"*4\r\n$6\r\nEXISTS\r\n$1\r\na\r\n$1\r\na\r\n$1\r\nz\r\n", b":2\r\n").await;
    reply(&mut stream, b"*4\r\n$3\r\nDEL\r\n$1\r\na\r\n$1\r\nb\r\n$1\r\nz\r\n", b":2\r\n").await;
    reply(&mut stream, b"*2\r\n$6\r\nexists\r\n$1\r\na\r\n", b":0\r\n").await;
    reply(&mut stream, b"*2\r\n$3\r\nGET\r\n$1\r\nb\r\n", b"$-1\r\n").await;
}

#[tokio::test]
async fn hidden_client() {
    let addr = start().await;
    let mut client = Client::connect(addr).await.unwrap();
    client.set("x", "1".into()).await.unwrap();
    let keys = vec!["x".to_string(), "y".to_string()];
    assert_eq!(client.exists(&keys).await.unwrap(), 1);
    assert_eq!(client.del(&keys).await.unwrap(), 1);
    assert_eq!(client.exists(&keys).await.unwrap(), 0);
}

#[test]
fn hidden_blocking_client() {
    let runtime = tokio::runtime::Runtime::new().unwrap();
    let addr = runtime.block_on(start());
    let mut client = BlockingClient::connect(addr).unwrap();
    client.set("k", "v".into()).unwrap();
    let keys = vec!["k".to_string()];
    assert_eq!(client.exists(&keys).unwrap(), 1);
    assert_eq!(client.del(&keys).unwrap(), 1);
    assert_eq!(client.del(&keys).unwrap(), 0);
}
"""


def fail(message):
    print(f"FAIL: {message}")
    raise SystemExit(1)


def check():
    Path("tests/hidden_del_exists.rs").write_text(TEST)
    subprocess.run(["cargo", "build", "-q", "--bins"], check=True)
    if subprocess.run(["cargo", "test", "-q"]).returncode:
        fail("tests failed")
    binaries = Path(os.environ.get("CARGO_TARGET_DIR", "target")) / "debug"
    server = subprocess.Popen([binaries / "mini-redis-server", "--port", "6390"])
    try:
        for _ in range(50):
            try:
                socket.create_connection(("127.0.0.1", 6390)).close()
                break
            except OSError:
                time.sleep(0.1)

        def cli(*args):
            return subprocess.run(
                [binaries / "mini-redis-cli", "--port", "6390", *args],
                capture_output=True,
                text=True,
            ).stdout

        cli("set", "p", "1")
        cli("set", "q", "2")
        for args, want in [
            (("exists", "p", "q", "r"), "(integer) 2\n"),
            (("del", "p", "r"), "(integer) 1\n"),
            (("exists", "p"), "(integer) 0\n"),
        ]:
            if cli(*args) != want:
                fail(f"mini-redis-cli {' '.join(args)} printed {cli(*args)!r}")
    finally:
        server.kill()
    print("PASS")


if __name__ == "__main__":
    {"check": check}[sys.argv[1]]()
