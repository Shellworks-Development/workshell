-- Scenario run against the SDK by test/run.py. Bundled after mocks.luau and
-- the SDK modules, so `Workshell`, `ClientModule`, `advance` etc. are in scope.

local function check(cond, msg)
	if not cond then
		error("FAILED: " .. msg, 2)
	end
end

local alice = newPlayer(1001, "alice")
table.insert(playerList, alice)

Workshell.init({ url = "http://localhost:8080/", key = "wsk_test", signingSecret = "wss_secret", flushInterval = 5, heartbeatInterval = 30 })
check(Workshell.isEnabled(), "enabled after init")

-- Output capture
services.LogService.MessageOut:Fire("hello from print", Enum.MessageType.MessageOutput)
services.LogService.MessageOut:Fire("[Workshell] internal", Enum.MessageType.MessageOutput)

-- Uncaught errors, the second identical one should merge into a count
local scriptInst = instance("Script", { Name = "Combat", _path = "ServerScriptService.Combat" })
for _ = 1, 3 do
	services.ScriptContext.Error:Fire(
		"ServerScriptService.Combat:42: attempt to index nil with 'Ammo'",
		"ServerScriptService.Combat, line 42 - function fire\nServerScriptService.Main, line 3\n",
		scriptInst
	)
end

-- Handled errors
local ok = Workshell.try(function()
	local t = nil
	return t.field
end)
check(not ok, "try returns false on error")
Workshell.captureError("Datastore budget exhausted", { player = alice, tags = { store = "PlayerData" }, context = { attempt = 3 } })

-- Analytics
Workshell.track("level_up", { level = 5, weapon = scriptInst }, alice)
Workshell.track("coins_earned", { source = "quest" }, alice, 250)
Workshell.trackPurchase(alice, 99, { productId = 555, productName = "Gems" })
services.MarketplaceService.PromptGamePassPurchaseFinished:Fire(alice, 42, true)
Workshell.increment("shop.opened")
Workshell.increment("shop.opened", 2)
Workshell.gauge("queue.size", 7)
Workshell.time("datastore.load", function()
	return 1
end)
Workshell.info("round started", { map = "Desert" })

-- Session metadata, context, tracing, detections
Workshell.setUser(alice, { level = 12, guild = "Red" })
Workshell.setContext("round", { map = "Desert" })
local loaded = Workshell.trace("datastore.load", function()
	return "data"
end, alice)
check(loaded == "data", "trace returns fn results")
local span = Workshell.startSpan("shop.purchase", { player = alice })
span:setAttribute("item", "sword")
span:finish("error")
check(not pcall(Workshell.trace, "boom", function()
	error("trace failure")
end), "trace re-raises")
Workshell.flag(alice, "speed", { speed = 90, max = 32 }, { severity = "high", action = "kicked" })

-- Protected remote: a spammer gets rate limited and flagged once
local buy = Instance.new("RemoteEvent")
local handled = 0
Workshell.protect(buy, function(_player, itemId)
	handled += 1
end, { rate = 2, burst = 3, validate = function(_player, itemId)
	return typeof(itemId) == "string"
end })
for _ = 1, 10 do
	buy.OnServerEvent:Fire(alice, "sword")
end
buy.OnServerEvent:Fire(alice, 42) -- invalid args, but already rate-limited
check(handled == 3, "protect allows the burst only, got " .. handled)

-- A second player joins, alice leaves
local bob = newPlayer(1002, "bob")
table.insert(playerList, bob)
services.Players.PlayerAdded:Fire(bob)

-- Client side: bob's client reports an error, an event, and a forged purchase
testState.localPlayer = bob
local Client = loadClient()
Client.track("opened_menu", { menu = "shop" })
Client.captureError("Players.bob.PlayerGui.UI:10: oops")
remote():FireServer("event", { name = "$purchase", value = 1000000 })
remote():FireServer("event", 12345) -- malformed
for _ = 1, 100 do
	remote():FireServer("event", { name = "spam" }) -- rate limited
end

services.Players.PlayerRemoving:Fire(alice)
table.remove(playerList, 1)

-- First flush fails, then succeeds after backoff
testState.failNextPost = true
advance(6)
check(#posts == 0, "first post failed")
advance(30)
check(#posts >= 1, "retried after failure")

-- Shutdown
for _, fn in closeHandlers do
	task.spawn(fn)
end
advance(5)

local all = {}
for _, p in posts do
	check(p.headers["X-Workshell-Key"] == "wsk_test", "ingest key header")
	check(p.url == "http://localhost:8080/api/v1/ingest", "endpoint url: " .. p.url)
	check(p.compress == true, "gzip enabled")
	print("POST " .. p.headers["X-Workshell-Timestamp"] .. " " .. p.headers["X-Workshell-Signature"] .. " " .. p.body)
end
print("OK")
