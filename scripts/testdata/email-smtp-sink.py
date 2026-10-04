"""Disposable TLS sink for email-compose-check.py; never opens outbound sockets."""
import base64
import json
from pathlib import Path
import socket
import ssl
import threading
import uuid

root = Path("/fixture")
auth = json.loads((root / "auth.json").read_text())
expected_auth = base64.b64encode(
    ("\0" + auth["username"] + "\0" + auth["password"]).encode()
)
tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
tls.minimum_version = ssl.TLSVersion.TLSv1_2
tls.load_cert_chain(root / "cert.pem", root / "key.pem")
lock = threading.Lock()


def serve(conn):
    try:
        conn.settimeout(12)
        with tls.wrap_socket(conn, server_side=True) as secured:
            stream = secured.makefile("rwb")

            def reply(line):
                stream.write(line + b"\r\n")
                stream.flush()

            reply(b"220 owned synthetic TLS sink")
            authenticated = False
            recipient = None
            while True:
                line = stream.readline(8192).rstrip(b"\r\n")
                if not line:
                    return
                if line.startswith(b"EHLO"):
                    reply(b"250-owned\r\n250 AUTH PLAIN")
                elif line.startswith(b"AUTH PLAIN "):
                    authenticated = line.split(b" ", 2)[2] == expected_auth
                    reply(b"235 authenticated" if authenticated else b"535 denied")
                elif line.startswith(b"MAIL FROM:"):
                    reply(b"250 sender" if authenticated else b"530 auth required")
                elif line.startswith(b"RCPT TO:"):
                    recipient = line.removeprefix(b"RCPT TO:").strip()
                    code = int((root / "rcpt-code").read_text())
                    reply(str(code).encode() + b" synthetic recipient")
                    with lock:
                        with (root / "attempts").open("a") as attempts:
                            attempts.write(str(code) + "\n")
                elif line == b"DATA" and authenticated and recipient:
                    reply(b"354 data")
                    data = bytearray()
                    while True:
                        part = stream.readline(8192)
                        if not part:
                            return
                        if part == b".\r\n":
                            break
                        data.extend(part[1:] if part.startswith(b"..") else part)
                        if len(data) > 256 * 1024:
                            return
                    (root / (str(uuid.uuid4()) + ".eml")).write_bytes(data)
                    reply(b"250 accepted")
                elif line == b"QUIT":
                    reply(b"221 bye")
                    return
                else:
                    reply(b"500 unsupported")
    except (OSError, ssl.SSLError, ValueError):
        conn.close()


listener = socket.socket()
listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
listener.bind(("0.0.0.0", 465))
listener.listen(16)
(root / "ready").touch()
while True:
    connection, _ = listener.accept()
    threading.Thread(target=serve, args=(connection,), daemon=True).start()
