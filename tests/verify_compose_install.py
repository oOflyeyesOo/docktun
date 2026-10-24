"""Regression check for source checkout and portable, optional environment configuration."""
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = (
    "Dockerfile",
    "docker-compose.yml",
    "frontend/package.json",
    "frontend/pnpm-lock.yaml",
    "frontend/pnpm-workspace.yaml",
    "backend/go.mod",
    "backend/go.sum",
)
for name in REQUIRED:
    assert (ROOT / name).is_file(), f"Missing required source build file: {name}"

# A fresh checkout has no .env file. Avoid host-specific variables during this check.
clean_env = dict(os.environ)
for key in ("ADMIN_USERNAME", "ADMIN_PASSWORD", "JWT_SECRET", "PORT", "DB_PATH",
            "CLOUDFLARED_CONFIG_PATH", "LISTEN_ADDR", "SELF_PATH"):
    clean_env.pop(key, None)

def compose_config(env):
    output = subprocess.check_output(
        ["docker", "compose", "config", "--format", "json"],
        cwd=ROOT,
        env=env,
        text=True,
    )
    return json.loads(output)

base = compose_config(clean_env)
docktun = base["services"]["docktun"]
settings = docktun["environment"]
assert settings["ADMIN_USERNAME"] == "admin"
assert settings["ADMIN_PASSWORD"] == ""
assert settings["JWT_SECRET"] == ""
assert docktun["build"]["dockerfile"] == "Dockerfile"
assert Path(docktun["build"]["context"]).resolve() == ROOT.resolve()
assert not docktun.get("env_file"), "A required env_file breaks fresh installs and old Compose"

overrides = dict(clean_env, ADMIN_USERNAME="test-owner", PORT="18081")
changed = compose_config(overrides)
assert changed["services"]["docktun"]["environment"]["ADMIN_USERNAME"] == "test-owner"
assert any(port.get("published") == "18081" for port in changed["services"]["docktun"]["ports"])
print("Portable Docker Compose and full-source build context checks passed")
