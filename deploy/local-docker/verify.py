"""Verify the running image against an independently supplied build manifest."""
import hashlib
import http.cookiejar
import json
from pathlib import Path
import sys
import urllib.request


def verify(expected):
    base = "http://127.0.0.1:8080"
    opener = urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def get(path, data=None):
        request = urllib.request.Request(base + path)
        if data is not None:
            request.data = json.dumps(data).encode()
            request.add_header("Content-Type", "application/json")
        with opener.open(request, timeout=15) as response:
            assert response.status == 200, (path, response.status)
            return response.read(), response.headers

    for name, digest in expected["binaries"].items():
        actual = hashlib.sha256(Path("/opt/clusterforge/platform", name).read_bytes()).hexdigest()
        assert actual == digest, "binary mismatch: " + name
    for path, digest in expected["assets"].items():
        body, _ = get("/" + path)
        assert hashlib.sha256(body).hexdigest() == digest, "asset mismatch: " + path
    version, headers = get("/version.json")
    assert json.loads(version)["version"] == expected["uiVersion"], "UI version mismatch"
    assert headers.get("Cache-Control") == "no-store", "version must not be cached"
    users, _ = get("/api/v1/session/users")
    admin = next(user for user in json.loads(users)["items"] if user["role"] == "platform_admin")
    get("/api/v1/session/switch", {"userId": admin["id"]})
    health, _ = get("/api/v1/executor-health")
    health = json.loads(health)["data"]
    assert (health["status"], health["ansible"], health["python"]) == (
        "passed", "2.8.8", "3.6.9"), health
    return {"uiVersion": expected["uiVersion"], "matchingAssets": len(expected["assets"]),
            "sourceSha256": expected["sourceSha256"], "executor": health}


if __name__ == "__main__":
    print(json.dumps(verify(json.loads(sys.argv[1])), ensure_ascii=False))
