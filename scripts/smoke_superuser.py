#!/usr/bin/env python3
"""Exercise createsuperuser through a PTY and a synthetic Unix-socket peer."""
import json
import os
from pathlib import Path
import pty
import select
import socket
import subprocess
import tempfile
import threading
import time

binary = os.environ.get('VEDA_TEST_BINARY', str(Path(__file__).resolve().parents[1] / 'veda'))
with tempfile.TemporaryDirectory(prefix='veda-superuser-') as tmp:
    path = os.path.join(tmp, 'socket')
    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(path)
    server.listen(1)
    errors = []
    def serve():
        try:
            conn, _ = server.accept()
            with conn, conn.makefile('rb') as stream:
                headers = {}
                request = stream.readline()
                while line := stream.readline():
                    if line in (b'\r\n', b'\n'): break
                    key, value = line.decode().split(':', 1)
                    headers[key.lower()] = value.strip()
                data = json.loads(stream.read(int(headers['content-length'])))
                if request.split()[1] != b'/api/local/createsuperuser' or data != {'id':'fixture', 'password':'synthetic long password'}:
                    raise ValueError('unexpected request')
                conn.sendall(b'HTTP/1.1 201 Created\r\nContent-Length: 0\r\nConnection: close\r\n\r\n')
        except Exception as exc:
            errors.append(type(exc).__name__)
    worker = threading.Thread(target=serve, daemon=True); worker.start()
    master, slave = pty.openpty()
    proc = subprocess.Popen([binary, 'createsuperuser', '--username', 'fixture', '--socket', path], stdin=slave, stdout=slave, stderr=slave, close_fds=True)
    os.close(slave)
    output = bytearray()
    def wait_for(marker):
        deadline = time.monotonic() + 10
        while marker not in output:
            if time.monotonic() > deadline: raise RuntimeError('prompt timeout')
            ready, _, _ = select.select([master], [], [], 1)
            if ready: output.extend(os.read(master, 4096))
    try:
        wait_for(b'Password: '); os.write(master,b'synthetic long password\n')
        wait_for(b'Password (again): '); os.write(master,b'synthetic long password\n')
        wait_for(b'Administrator account created.')
        if proc.wait(timeout=10) != 0 or b'synthetic long password' in output: raise RuntimeError('CLI failed or echoed password')
        worker.join(timeout=5)
        if worker.is_alive() or errors: raise RuntimeError('synthetic socket failed')
        print('createsuperuser PTY, password confirmation, echo suppression and Unix request: passed')
    finally:
        if proc.poll() is None: proc.terminate(); proc.wait(timeout=5)
        os.close(master); server.close()
