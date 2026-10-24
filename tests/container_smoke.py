"""Isolated production-image lifecycle check. Never mounts the host Docker socket."""
import json
import os
import subprocess
import time
import urllib.error
import urllib.request
import uuid

name = "docktun-smoke-" + uuid.uuid4().hex[:12]
volume = name + "-data"
base = "http://127.0.0.1:18080"

def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()

def request(path, body=None, token=None, expected=200):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
    try:
        response = urllib.request.urlopen(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        assert response.code == expected, (path, response.code, expected)
        payload = response.read()
        return json.loads(payload) if payload and response.headers.get_content_type() == "application/json" else None

def ready():
    for _ in range(60):
        try:
            request("/healthz")
            return
        except (OSError, AssertionError):
            time.sleep(1)
    raise RuntimeError("container did not become ready")

try:
    docker("volume", "create", volume)
    docker("run", "-d", "--name", name, "-p", "127.0.0.1:18080:3001", "-v", volume + ":/app/data", os.environ.get("DOCKTUN_TEST_IMAGE", "docktun:test"))
    ready()
    assert request("/api/setup/status")["completed"] is False
    credentials = {"admin_username": "owner", "admin_password": "test-password-at-least-12"}
    request("/api/setup/complete", credentials, expected=401)
    setup_token = docker("exec", name, "cat", "/app/data/setup.token")
    session = request("/api/setup/complete", {**credentials, "setup_token": setup_token})
    access = session["access_token"]
    request("/api/setup/complete", {**credentials, "setup_token": setup_token}, expected=409)
    request("/api/setup/reconfigure", {}, expected=401)
    request("/api/diagnostics", expected=401)
    request("/api/setup/reconfigure", {"admin_password": "replacement-password"}, access, expected=400)
    assert request("/api/setup/reconfigure", {}, access)["access_token"] == ""
    request("/api/setup/details", token=access)
    diagnostics = request("/api/diagnostics", token=access)
    assert diagnostics["checks"][0]["ok"] is False
    assert diagnostics["checks"][1]["ok"] is True
    login = {"username": "owner", "password": credentials["admin_password"]}
    request("/auth/login", login)
    docker("restart", name)
    ready()
    assert request("/api/setup/status?force=true")["completed"] is True
    request("/auth/login", login)
    request("/api/setup/complete", credentials, expected=409)
    print("Container smoke passed: ownership, authentication, reconfiguration, restart persistence")
finally:
    subprocess.run(["docker", "rm", "-f", name], check=False, stdout=subprocess.DEVNULL)
    subprocess.run(["docker", "volume", "rm", volume], check=False, stdout=subprocess.DEVNULL)
