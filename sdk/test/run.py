#!/usr/bin/env python3
"""Runs the SDK against Roblox mocks under the `luau` CLI.

    python3 sdk/test/run.py                      # run + assert
    python3 sdk/test/run.py --post URL KEY       # also send the batches to a server
"""
import json, os, re, subprocess, sys, tempfile, urllib.request

here = os.path.dirname(os.path.abspath(__file__))
src = os.path.join(here, "..", "src")

def module(var, name, parent, path):
    code = open(os.path.join(src, path)).read()
    code = re.sub(r"^(--!\w+\n)+", "", code)
    code = re.sub(r"^export type", "type", code, flags=re.M)
    return f"""
local {var} = instance("ModuleScript", {{ Name = "{name}" }})
{var}.Parent = {parent}
{var}._loader = function(script)
{code}
end
"""

bundle = open(os.path.join(here, "mocks.luau")).read()
bundle += """
local function require(m)
	if not rawget(m, "_loaded") then
		rawset(m, "_value", m._loader(m))
		rawset(m, "_loaded", true)
	end
	return m._value
end
local Root = instance("Folder", { Name = "ReplicatedStorage" })
"""
bundle += module("WorkshellModule", "Workshell", "Root", "init.luau")
bundle += module("SharedModule", "Shared", "WorkshellModule", "Shared.luau")
bundle += module("CryptoModule", "Crypto", "WorkshellModule", "Crypto.luau")
bundle += module("ServerModule", "Server", "WorkshellModule", "Server.luau")
bundle += module("ClientModule", "Client", "WorkshellModule", "Client.luau")
bundle += """
local Workshell = require(WorkshellModule)
local function loadClient()
	return require(ClientModule)
end
local function remote()
	return WorkshellModule.WorkshellRemote
end
"""
bundle += open(os.path.join(here, "spec.luau")).read()

with tempfile.NamedTemporaryFile("w", suffix=".luau", delete=False) as f:
    f.write(bundle)
    path = f.name
proc = subprocess.run(["luau", path], capture_output=True, text=True)
os.unlink(path)
out = proc.stdout + proc.stderr
if proc.returncode != 0 or not out.rstrip().endswith("OK"):
    print(out)
    sys.exit(1)

import hashlib, hmac as _hmac
batches = []
for line in out.splitlines():
    if line.startswith("POST "):
        rest = line[5:]
        ts, sig, body = rest.split(" ", 2)
        want = "v1=" + _hmac.new(b"wss_secret", (ts + "." + body).encode(), hashlib.sha256).hexdigest()
        assert sig == want, f"bad signature {sig} != {want}"
        batches.append(json.loads(body))
items = [i for b in batches for i in b["items"]]
types = {}
for i in items:
    types[i["type"]] = types.get(i["type"], 0) + 1
print(f"{len(batches)} batches, items by type: {types}")

errors = [i for i in items if i["type"] == "error"]
combat = [e for e in errors if "Ammo" in e["message"]]
assert len(combat) == 1 and combat[0]["count"] == 3, combat
assert any(e["side"] == "client" and e["userId"] == 1002 for e in errors), "client error relayed"
events = [i for i in items if i["type"] == "event"]
names = [e["name"] for e in events]
assert names.count("$purchase") == 2, names  # dev product + game pass, forged client one rejected
assert "opened_menu" in names
assert names.count("spam") < 25, "client events rate limited"
assert any(i["type"] == "session_update" and i.get("country") == "AU" for i in items)
assert any(i["type"] == "session_update" and i.get("platform") == "Desktop" for i in items)
assert any(i["type"] == "heartbeat" for i in items)
assert any(i["type"] == "server_close" for i in items)
assert not any("[Workshell]" in i.get("message", "") for i in items), "SDK output not captured"
metrics = {i["name"]: i for i in items if i["type"] == "metric"}
spans = {i["name"]: i for i in items if i["type"] == "span"}
assert spans["datastore.load"]["status"] == "ok" and spans["shop.purchase"]["status"] == "error" and spans["boom"]["status"] == "error", spans
assert spans["shop.purchase"]["attrs"]["item"] == "sword"
dets = [i["kind"] for i in items if i["type"] == "detection"]
assert "speed" in dets and dets.count("remote_spam") == 1, dets
alice = [i for i in items if i.get("userId") == 1001 and i["type"] in ("event", "span", "detection")]
assert alice and all(i.get("sessionId") for i in alice), "player items carry the session id"
assert any(i["type"] == "session_update" and (i.get("attrs") or {}).get("guild") == "Red" for i in items)
assert any((i.get("context") or {}).get("round", {}).get("map") == "Desert" for i in items if i["type"] == "error")
assert metrics["shop.opened"]["sum"] == 3 and metrics["shop.opened"]["count"] == 2, metrics
print("SDK test passed")

if len(sys.argv) == 4 and sys.argv[1] == "--post":
    url, key = sys.argv[2].rstrip("/"), sys.argv[3]
    for b in batches:
        req = urllib.request.Request(url + "/api/v1/ingest", data=json.dumps(b).encode(),
                                     headers={"Content-Type": "application/json", "X-Workshell-Key": key})
        print(urllib.request.urlopen(req).read().decode())
