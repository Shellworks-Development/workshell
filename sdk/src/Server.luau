--!nonstrict
-- Server half of the Workshell SDK: collects telemetry and ships it to the
-- Workshell server in gzip-compressed (optionally signed) batches.

local HttpService = game:GetService("HttpService")
local LocalizationService = game:GetService("LocalizationService")
local LogService = game:GetService("LogService")
local MarketplaceService = game:GetService("MarketplaceService")
local Players = game:GetService("Players")
local RunService = game:GetService("RunService")
local ScriptContext = game:GetService("ScriptContext")
local Stats = game:GetService("Stats")

local Shared = require(script.Parent.Shared)
local Crypto = require(script.Parent.Crypto)

export type Options = {
	-- Base URL of your Workshell server, e.g. "https://workshell.example.com".
	url: string,
	-- Project ingest key ("wsk_..."), or a Secret from HttpService:GetSecret().
	key: any,
	-- Optional request signing secret ("wss_..."), from project settings → Security.
	signingSecret: string?,

	-- Send data while testing in Studio (default false).
	enableInStudio: boolean?,
	-- Environment name shown in the dashboard (default "production", or "studio").
	environment: string?,
	-- Release identifier. Defaults to a WorkshellRelease module (written by
	-- `workshell release`) if one exists, else game.PlaceVersion.
	release: string?,

	captureErrors: boolean?, -- uncaught server errors (default true)
	captureOutput: boolean?, -- server output as logs (default true)
	outputLevel: string?, -- minimum output level: "debug" | "info" | "warn" | "error" (default "info")
	trackSessions: boolean?, -- player sessions (default true)
	trackGamePasses: boolean?, -- auto-track game pass purchases (default true)
	clientReporting: boolean?, -- accept errors/events/perf from clients (default true)

	flushInterval: number?, -- seconds between sends (default 5)
	heartbeatInterval: number?, -- seconds between server health reports (default 30)
	maxLogsPerMinute: number?, -- output lines per minute (default 600)
	debug: boolean?,

	-- Called before an error is sent. Return nil to drop it, or the (modified) item.
	beforeSendError: ((item: { [string]: any }) -> { [string]: any }?)?,
}

local LEVELS = { debug = 1, info = 2, warn = 3, error = 4 }
local MAX_QUEUE = 10000
local MAX_BATCH = 1000
local MAX_BREADCRUMBS = 30

local Server = {}

local state = {
	started = false,
	enabled = false,
	options = nil :: Options?,
	endpoint = "",
	queue = {} :: { any },
	breadcrumbs = {} :: { any },
	tags = {} :: { [string]: any },
	context = {} :: { [string]: any },
	metrics = {} :: { [string]: any },
	-- Errors already queued this flush window, keyed by message+stack, so that
	-- an error thrown every frame becomes one item with a count.
	errorWindow = {} :: { [string]: any },
	sessions = {} :: { [Player]: string },
	sessionsByUser = {} :: { [number]: string },
	logsThisMinute = 0,
	logWindowStart = 0,
	flushing = false,
	backoffUntil = 0,
	failures = 0,
	serverInfo = nil :: any,
	closing = false,
	warned = {} :: { [string]: boolean },
}

local function debugPrint(...)
	if state.options and state.options.debug then
		print(Shared.LOG_PREFIX, ...)
	end
end

local function warnOnce(key: string, message: string)
	if not state.warned[key] then
		state.warned[key] = true
		warn(Shared.LOG_PREFIX .. " " .. message)
	end
end

local function userIdOf(player: any): number?
	if typeof(player) == "Instance" and player:IsA("Player") then
		return player.UserId
	elseif typeof(player) == "number" then
		return player
	end
	return nil
end

local function push(item: { [string]: any })
	if not state.enabled then
		return
	end
	item.ts = item.ts or Shared.now()
	-- Tie the item to the player's current session for timelines.
	if item.userId and not item.sessionId then
		item.sessionId = state.sessionsByUser[item.userId]
	end
	local q = state.queue
	if #q >= MAX_QUEUE then
		table.remove(q, 1)
		warnOnce("queueFull", "event queue is full; dropping oldest items. Is the Workshell server reachable?")
	end
	table.insert(q, item)
end

local function addBreadcrumb(category: string, message: string, level: string?, data: any?)
	local crumbs = state.breadcrumbs
	table.insert(crumbs, {
		ts = Shared.now(),
		category = category,
		message = Shared.truncate(message, 300),
		level = level,
		data = Shared.dict(data),
	})
	if #crumbs > MAX_BREADCRUMBS then
		table.remove(crumbs, 1)
	end
end

local function merged(base: { [string]: any }, extra: { [string]: any }?)
	local out = table.clone(base)
	if extra then
		for k, v in extra do
			out[k] = v
		end
	end
	return Shared.dict(out)
end

-- Queues an error, merging repeats within the current flush window.
local function queueError(item: { [string]: any })
	local options = state.options
	if options and options.beforeSendError then
		local ok, result = pcall(options.beforeSendError, item)
		if not ok then
			warnOnce("beforeSendError", "beforeSendError failed: " .. tostring(result))
		elseif result == nil then
			return
		else
			item = result
		end
	end

	local key = (item.side or "server") .. "\0" .. tostring(item.message) .. "\0" .. tostring(item.stack) .. "\0" .. tostring(item.userId)
	local existing = state.errorWindow[key]
	if existing then
		existing.count += 1
		return
	end
	item.type = "error"
	item.count = 1
	item.message = Shared.truncate(tostring(item.message), 4000)
	item.stack = Shared.truncate(tostring(item.stack or ""), 8000)
	state.errorWindow[key] = item
	push(item)
	addBreadcrumb("error", item.message, "error")
end

---------------------------------------------------------------------------
-- Release detection
---------------------------------------------------------------------------

-- Finds the WorkshellRelease module written by `workshell release`.
local function detectRelease(): any?
	for _, container in { game:GetService("ServerStorage"), game:GetService("ReplicatedStorage"), game:GetService("ServerScriptService") } do
		local ok, module = pcall(function()
			return container:FindFirstChild("WorkshellRelease", true)
		end)
		if ok and module and module:IsA("ModuleScript") then
			local okRequire, info = pcall(require, module)
			if okRequire and typeof(info) == "table" and typeof(info.version) == "string" then
				return info
			end
		end
	end
	return nil
end

---------------------------------------------------------------------------
-- Transport
---------------------------------------------------------------------------

local function serverInfo()
	if state.serverInfo then
		return state.serverInfo
	end
	local serverType = "public"
	if RunService:IsStudio() then
		serverType = "studio"
	elseif game.PrivateServerId ~= "" then
		serverType = if game.PrivateServerOwnerId ~= 0 then "private" else "reserved"
	end
	local jobId = game.JobId
	if jobId == "" then
		jobId = "studio-" .. HttpService:GenerateGUID(false)
	end
	local release = state.options and state.options.release
	if not release then
		local info = detectRelease()
		release = if info then info.version else tostring(game.PlaceVersion)
	end
	state.serverInfo = {
		jobId = jobId,
		placeId = game.PlaceId,
		placeVersion = game.PlaceVersion,
		release = release,
		serverType = serverType,
		environment = (state.options and state.options.environment)
			or (if RunService:IsStudio() then "studio" else "production"),
		maxPlayers = Players.MaxPlayers,
	}
	return state.serverInfo
end

local function drainMetrics()
	for name, m in state.metrics do
		push({ type = "metric", name = name, kind = m.kind, count = m.count, sum = m.sum, min = m.min, max = m.max, last = m.last })
	end
	table.clear(state.metrics)
end

-- Removes the first n items from the queue.
local function dropFront(n: number)
	local q = state.queue
	state.queue = table.move(q, n + 1, #q, 1, table.create(math.max(#q - n, 0)))
end

-- Sends one batch. Returns true if the queue was (at least partly) drained.
local function sendBatch(): boolean
	local q = state.queue
	if #q == 0 then
		return true
	end
	local count = math.min(#q, MAX_BATCH)
	local items = table.move(q, 1, count, 1, {})

	local okEncode, body = pcall(HttpService.JSONEncode, HttpService, {
		sdk = { name = "workshell-luau", version = Shared.VERSION },
		server = serverInfo(),
		items = items,
	})
	if not okEncode then
		-- Should not happen since everything is sanitized; drop the batch rather than wedge the queue.
		warnOnce("encode", "failed to encode batch, dropping it: " .. tostring(body))
		dropFront(count)
		return false
	end

	local headers = {
		["Content-Type"] = "application/json",
		["X-Workshell-Key"] = state.options.key,
	}
	local secret = state.options.signingSecret
	if secret then
		local ts = tostring(DateTime.now().UnixTimestamp)
		headers["X-Workshell-Timestamp"] = ts
		headers["X-Workshell-Signature"] = "v1=" .. Crypto.hmacSha256Hex(secret, ts .. "." .. body)
	end

	local okRequest, response = pcall(HttpService.RequestAsync, HttpService, {
		Url = state.endpoint,
		Method = "POST",
		Headers = headers,
		Body = body,
		Compress = Enum.HttpCompression.Gzip,
	})
	if okRequest and response.Success then
		-- Remove exactly the items we sent; new items may have been pushed meanwhile.
		dropFront(count)
		state.failures = 0
		debugPrint(("sent %d items"):format(count))
		return true
	end

	local status = if okRequest then response.StatusCode else 0
	if status == 401 or status == 403 then
		warn(Shared.LOG_PREFIX .. " the Workshell server rejected the request (" .. tostring(response.Body) .. "); telemetry is disabled for this server.")
		state.enabled = false
		state.queue = {}
		return false
	elseif status == 413 or status == 400 then
		-- The server will never accept this batch; drop it.
		warnOnce("rejected" .. status, "batch rejected: " .. tostring(response.Body))
		dropFront(count)
		return true
	elseif not okRequest and tostring(response):find("Http requests are not enabled") then
		warnOnce("httpDisabled", "HTTP requests are disabled. Enable them in Game Settings > Security.")
	else
		warnOnce("sendFailed", "failed to reach Workshell (will keep retrying): " .. (if okRequest then "HTTP " .. status else tostring(response)))
	end
	state.failures += 1
	local retryAfter = okRequest and tonumber(response.Headers and (response.Headers["retry-after"] or response.Headers["Retry-After"]))
	state.backoffUntil = os.clock() + (retryAfter or math.min(2 ^ state.failures, 60))
	return false
end

local function flush(force: boolean?)
	if not state.enabled or state.flushing then
		return
	end
	if not force and os.clock() < state.backoffUntil then
		return
	end
	state.flushing = true
	drainMetrics()
	table.clear(state.errorWindow)
	while #state.queue > 0 do
		if not sendBatch() then
			break
		end
	end
	state.flushing = false
end

---------------------------------------------------------------------------
-- Collectors
---------------------------------------------------------------------------

local function startSession(player: Player)
	if state.sessions[player] then
		return
	end
	local sessionId = HttpService:GenerateGUID(false)
	state.sessions[player] = sessionId
	state.sessionsByUser[player.UserId] = sessionId
	push({ type = "session_start", userId = player.UserId, sessionId = sessionId, attrs = Shared.dict({
		accountAge = player.AccountAge,
		membership = player.MembershipType.Name,
	}) })
	addBreadcrumb("player", player.Name .. " joined")

	task.spawn(function()
		local ok, country = pcall(LocalizationService.GetCountryRegionForPlayerAsync, LocalizationService, player)
		if ok and country and state.sessions[player] == sessionId then
			push({ type = "session_update", sessionId = sessionId, country = country })
		end
	end)
end

local function endSession(player: Player)
	local sessionId = state.sessions[player]
	if not sessionId then
		return
	end
	push({ type = "session_end", userId = player.UserId, sessionId = sessionId })
	state.sessions[player] = nil
	state.sessionsByUser[player.UserId] = nil
	addBreadcrumb("player", player.Name .. " left")
end

local function heartbeatLoop(interval: number)
	local frames, elapsed = 0, 0
	RunService.Heartbeat:Connect(function(dt)
		frames += 1
		elapsed += dt
	end)
	while state.enabled and not state.closing do
		task.wait(interval)
		local fps = if elapsed > 0 then frames / elapsed else 0
		frames, elapsed = 0, 0

		local players = Players:GetPlayers()
		local pingTotal, pingCount = 0, 0
		for _, p in players do
			local ok, ping = pcall(p.GetNetworkPing, p)
			if ok and typeof(ping) == "number" then
				pingTotal += ping
				pingCount += 1
			end
		end

		push({
			type = "heartbeat",
			players = #players,
			fps = math.floor(fps * 10 + 0.5) / 10,
			heartbeatMs = Stats.HeartbeatTimeMs,
			memoryMb = Stats:GetTotalMemoryUsageMb(),
			-- GetNetworkPing is one-way seconds; report round-trip ms.
			pingMs = if pingCount > 0 then pingTotal / pingCount * 2000 else 0,
			instances = Stats.InstanceCount,
			sendKbps = Stats.DataSendKbps,
			recvKbps = Stats.DataReceiveKbps,
		})
	end
end

local OUTPUT_LEVELS = {
	[Enum.MessageType.MessageOutput] = "info",
	[Enum.MessageType.MessageInfo] = "info",
	[Enum.MessageType.MessageWarning] = "warn",
	[Enum.MessageType.MessageError] = "error",
}

local function onOutput(message: string, messageType: Enum.MessageType)
	if message:sub(1, #Shared.LOG_PREFIX) == Shared.LOG_PREFIX then
		return
	end
	local level = OUTPUT_LEVELS[messageType] or "info"
	if LEVELS[level] < LEVELS[state.options.outputLevel or "info"] then
		return
	end
	addBreadcrumb("output", message, level)

	local now = os.clock()
	if now - state.logWindowStart >= 60 then
		state.logWindowStart = now
		state.logsThisMinute = 0
	end
	state.logsThisMinute += 1
	local limit = state.options.maxLogsPerMinute or 600
	if state.logsThisMinute > limit then
		if state.logsThisMinute == limit + 1 then
			push({ type = "log", level = "warn", source = "workshell", message = ("Output rate limit reached (%d/min); dropping lines for the rest of this minute."):format(limit) })
		end
		return
	end
	push({ type = "log", level = level, source = "output", message = Shared.truncate(message, 4000) })
end

local function onScriptError(message: string, stack: string, origin: Instance?)
	local scriptName = ""
	if origin then
		local ok, name = pcall(origin.GetFullName, origin)
		scriptName = if ok then name else tostring(origin)
	end
	queueError({
		side = "server",
		message = message,
		stack = stack,
		script = scriptName,
		tags = merged(state.tags),
		context = merged(state.context),
		breadcrumbs = table.clone(state.breadcrumbs),
	})
end

---------------------------------------------------------------------------
-- Exploit detections
---------------------------------------------------------------------------

export type FlagOptions = {
	severity: string?, -- "low" | "medium" (default) | "high"
	action: string?, -- what you did about it: "kicked", "banned", "rollback", ...
}

local function flag(player: any, kind: string, details: { [string]: any }?, opts: FlagOptions?)
	local o = opts or {}
	push({
		type = "detection",
		userId = userIdOf(player),
		kind = Shared.truncate(tostring(kind), 64),
		severity = o.severity or "medium",
		action = o.action,
		details = Shared.dict(details),
	})
	addBreadcrumb("security", tostring(kind), "warn", details)
end

---------------------------------------------------------------------------
-- Client reporting
---------------------------------------------------------------------------

local CLIENT_BUDGET = 20 -- messages
local CLIENT_REFILL = 2 -- messages per second

local function setupRemote()
	local remote = Instance.new("RemoteEvent")
	remote.Name = Shared.REMOTE_NAME
	local buckets: { [Player]: { tokens: number, at: number, flagged: number } } = {}

	remote.OnServerEvent:Connect(function(player: Player, kind: any, payload: any)
		-- Token-bucket rate limit per player so a malicious client can't flood us.
		local now = os.clock()
		local b = buckets[player]
		if not b then
			b = { tokens = CLIENT_BUDGET, at = now, flagged = -math.huge }
			buckets[player] = b
		end
		b.tokens = math.min(CLIENT_BUDGET, b.tokens + (now - b.at) * CLIENT_REFILL)
		b.at = now
		if b.tokens < 1 then
			if now - b.flagged > 60 then
				b.flagged = now
				flag(player, "workshell_remote_spam", { remote = Shared.REMOTE_NAME }, { severity = "low" })
			end
			return
		end
		b.tokens -= 1
		if typeof(kind) ~= "string" or typeof(payload) ~= "table" then
			flag(player, "workshell_invalid_payload", { kind = typeof(kind), payload = typeof(payload) }, { severity = "low" })
			return
		end

		local function str(v: any, max: number): string?
			return if typeof(v) == "string" then Shared.truncate(v, max) else nil
		end
		local function num(v: any): number?
			return if typeof(v) == "number" and v == v and v ~= math.huge and v ~= -math.huge then v else nil
		end

		if kind == "error" then
			local message = str(payload.message, 2000)
			if not message then
				return
			end
			queueError({
				side = "client",
				message = message,
				stack = str(payload.stack, 4000) or "",
				script = str(payload.script, 200) or "",
				userId = player.UserId,
				tags = merged(state.tags, { platform = str(payload.platform, 20) }),
				context = Shared.dict({ player = player.Name, displayName = player.DisplayName }),
				breadcrumbs = if typeof(payload.breadcrumbs) == "table" then Shared.sanitize(payload.breadcrumbs) else nil,
			})
		elseif kind == "event" then
			local name = str(payload.name, 100)
			if not name or name:sub(1, 1) == "$" then
				return -- clients may not send reserved events like $purchase
			end
			local props = if typeof(payload.props) == "table" then Shared.sanitize(payload.props) else {}
			props["$client"] = true
			push({ type = "event", name = name, userId = player.UserId, value = num(payload.value), props = Shared.dict(props) })
		elseif kind == "device" then
			local sessionId = state.sessions[player]
			local platform = str(payload.platform, 20)
			if sessionId and platform then
				push({ type = "session_update", sessionId = sessionId, platform = platform })
			end
		elseif kind == "perf" then
			local sessionId = state.sessions[player]
			local fps, memory, ping = num(payload.fps), num(payload.memoryMb), num(payload.pingMs)
			if sessionId and fps and memory then
				push({ type = "client_perf", sessionId = sessionId, fps = fps, memoryMb = memory, pingMs = ping or 0, platform = str(payload.platform, 20) })
			end
		end
	end)

	Players.PlayerRemoving:Connect(function(player)
		buckets[player] = nil
	end)
	remote.Parent = script.Parent
end

---------------------------------------------------------------------------
-- Public API
---------------------------------------------------------------------------

--[[
	Starts Workshell. Call once from a server Script, e.g.

		Workshell.init({ url = "https://workshell.example.com", key = "wsk_..." })
]]
function Server.init(options: Options)
	if state.started then
		warn(Shared.LOG_PREFIX .. " init() called more than once; ignoring.")
		return
	end
	assert(typeof(options) == "table", "Workshell.init expects an options table")
	assert(typeof(options.url) == "string" and options.url ~= "", "Workshell.init: `url` is required")
	assert((typeof(options.key) == "string" and options.key ~= "") or typeof(options.key) == "Secret", "Workshell.init: `key` is required")
	state.started = true
	state.options = options

	if RunService:IsStudio() and not options.enableInStudio then
		print(Shared.LOG_PREFIX .. " disabled in Studio (set enableInStudio = true to send data).")
		return
	end

	state.enabled = true
	state.endpoint = options.url:gsub("/+$", "") .. "/api/v1/ingest"

	if options.captureErrors ~= false then
		ScriptContext.Error:Connect(onScriptError)
	end
	if options.captureOutput ~= false then
		LogService.MessageOut:Connect(onOutput)
	end
	if options.trackSessions ~= false then
		Players.PlayerAdded:Connect(startSession)
		Players.PlayerRemoving:Connect(endSession)
		for _, p in Players:GetPlayers() do
			startSession(p)
		end
	end
	if options.trackGamePasses ~= false then
		MarketplaceService.PromptGamePassPurchaseFinished:Connect(function(player, passId, purchased)
			if not purchased then
				return
			end
			local ok, info = pcall(MarketplaceService.GetProductInfo, MarketplaceService, passId, Enum.InfoType.GamePass)
			Server.trackPurchase(player, if ok and info then info.PriceInRobux or 0 else 0, {
				kind = "gamepass",
				productId = passId,
				productName = if ok and info then info.Name else nil,
			})
		end)
	end
	if options.clientReporting ~= false then
		setupRemote()
	end

	task.spawn(heartbeatLoop, options.heartbeatInterval or 30)
	task.spawn(function()
		local interval = options.flushInterval or 5
		while state.enabled and not state.closing do
			task.wait(interval)
			flush()
		end
	end)

	game:BindToClose(function()
		state.closing = true
		for player in state.sessions do
			endSession(player)
		end
		push({ type = "server_close" })
		local deadline = os.clock() + 20
		while state.enabled and #state.queue > 0 and os.clock() < deadline do
			flush(true)
			if #state.queue > 0 then
				task.wait(1)
			end
		end
	end)

	push({ type = "log", level = "info", source = "workshell", message = "Server started", attrs = Shared.dict({ release = serverInfo().release }) })
	debugPrint("started", state.endpoint, "release", serverInfo().release)
end

-- Writes a structured log line. level: "debug" | "info" | "warn" | "error".
function Server.log(level: string, message: string, attrs: { [string]: any }?, player: Player?)
	if LEVELS[level] == nil then
		level = "info"
	end
	local text = tostring(message)
	addBreadcrumb("log", text, level)
	push({
		type = "log",
		level = level,
		message = Shared.truncate(text, 4000),
		attrs = Shared.dict(attrs),
		userId = userIdOf(player),
		source = "sdk",
	})
end

function Server.debug(message: string, attrs: { [string]: any }?, player: Player?)
	Server.log("debug", message, attrs, player)
end
function Server.info(message: string, attrs: { [string]: any }?, player: Player?)
	Server.log("info", message, attrs, player)
end
function Server.warn(message: string, attrs: { [string]: any }?, player: Player?)
	Server.log("warn", message, attrs, player)
end
function Server.error(message: string, attrs: { [string]: any }?, player: Player?)
	Server.log("error", message, attrs, player)
end

export type CaptureOptions = {
	player: Player?,
	level: string?, -- "error" (default) | "warning" | "fatal"
	tags: { [string]: any }?,
	context: { [string]: any }?,
	-- Custom grouping key: errors with the same fingerprint become one issue.
	fingerprint: string?,
	stack: string?,
}

-- Reports a handled error (anything: string, table, or error object) as an issue.
function Server.captureError(err: any, opts: CaptureOptions?)
	local o = opts or {}
	local message = if typeof(err) == "string" then err else tostring(err)
	queueError({
		side = "server",
		level = o.level,
		message = message,
		stack = o.stack or debug.traceback(nil, 2),
		userId = userIdOf(o.player),
		fingerprint = o.fingerprint,
		tags = merged(state.tags, o.tags),
		context = merged(state.context, o.context),
		breadcrumbs = table.clone(state.breadcrumbs),
	})
end

--[[
	Calls fn(...) and reports any error it throws, with a full stack trace.
	Returns the same values as pcall.

		local ok, result = Workshell.try(loadPlayerData, player)
]]
function Server.try(fn: (...any) -> ...any, ...: any): (boolean, ...any)
	local stack
	local results = table.pack(xpcall(fn, function(e)
		stack = debug.traceback(nil, 2)
		return e
	end, ...))
	if not results[1] then
		Server.captureError(results[2], { stack = stack })
	end
	return table.unpack(results, 1, results.n)
end

-- Returns a function that behaves like fn but reports errors (and re-raises them).
function Server.wrap<A..., R...>(fn: (A...) -> R...): (A...) -> R...
	return function(...)
		local results = table.pack(Server.try(fn, ...))
		if not results[1] then
			error(results[2], 0)
		end
		return table.unpack(results, 2, results.n)
	end :: any
end

--[[
	Tracks a custom analytics event.

		Workshell.track("level_up", { level = 5 }, player)
		Workshell.track("coins_earned", { source = "quest" }, player, 250)
]]
function Server.track(name: string, props: { [string]: any }?, player: Player?, value: number?)
	assert(typeof(name) == "string" and name ~= "", "Workshell.track: name must be a non-empty string")
	addBreadcrumb("event", name, nil, props)
	push({
		type = "event",
		name = Shared.truncate(name, 100),
		props = Shared.dict(props),
		userId = userIdOf(player),
		value = if typeof(value) == "number" and value == value then value else nil,
	})
end

--[[
	Records Robux revenue. Call it from your ProcessReceipt handler after granting
	the purchase (game passes are tracked automatically).
]]
function Server.trackPurchase(player: Player | number, robux: number, props: { [string]: any }?)
	local p = props and table.clone(props) or {}
	p.kind = p.kind or "product"
	push({ type = "event", name = "$purchase", userId = userIdOf(player), value = robux, props = Shared.dict(p) })
	addBreadcrumb("purchase", ("%s for R$%d"):format(tostring(p.productName or p.productId or "purchase"), robux))
end

--[[
	Attaches metadata to a player's current session (shown on their session and
	player pages), e.g. Workshell.setUser(player, { level = 12, guild = "Red" }).
]]
function Server.setUser(player: Player, attrs: { [string]: any })
	local sessionId = state.sessions[player]
	if sessionId then
		push({ type = "session_update", sessionId = sessionId, attrs = Shared.dict(attrs) })
	end
end

-- Sets context attached to every error from this server (e.g. map, round, mode).
-- Pass nil to remove a key.
function Server.setContext(key: string, value: any)
	state.context[key] = Shared.sanitize(value)
end

-- Sets a tag attached to every error from this server.
function Server.setTag(key: string, value: any)
	state.tags[key] = value
end

-- Adds a breadcrumb: context shown alongside the next errors.
function Server.addBreadcrumb(message: string, category: string?, data: { [string]: any }?)
	addBreadcrumb(category or "custom", message, nil, data)
end

---------------------------------------------------------------------------
-- Performance tracing
---------------------------------------------------------------------------

export type Span = {
	setAttribute: (self: Span, key: string, value: any) -> (),
	finish: (self: Span, status: string?) -> (),
	child: (self: Span, name: string) -> Span,
}

--[[
	Starts timing an operation. Spans show up under Performance with p50/p95
	durations, and on the player's session timeline.

		local span = Workshell.startSpan("datastore.load", { player = player })
		...
		span:finish()          -- or span:finish("error")
]]
function Server.startSpan(name: string, opts: { player: Player?, attrs: { [string]: any }?, traceId: string?, parent: string? }?): Span
	local o = opts or {}
	local started = os.clock()
	local id = HttpService:GenerateGUID(false)
	local attrs = o.attrs and table.clone(o.attrs) or {}
	local finished = false
	local span = {}
	function span:setAttribute(key: string, value: any)
		attrs[key] = value
	end
	function span:child(childName: string)
		return Server.startSpan(childName, { player = o.player, traceId = o.traceId or id, parent = id })
	end
	function span:finish(status: string?)
		if finished then
			return
		end
		finished = true
		push({
			type = "span",
			name = Shared.truncate(name, 120),
			durationMs = (os.clock() - started) * 1000,
			status = status or "ok",
			userId = userIdOf(o.player),
			traceId = o.traceId or id,
			spanId = id,
			parent = o.parent,
			attrs = Shared.dict(attrs),
		})
	end
	return span :: any
end

--[[
	Times fn(...) as a span and returns its results. If fn errors, the span is
	marked as an error and the error is reported and re-raised.

		local data = Workshell.trace("datastore.load", function() return store:GetAsync(key) end, player)
]]
function Server.trace(name: string, fn: (...any) -> ...any, player: Player?, ...: any): ...any
	local span = Server.startSpan(name, { player = player })
	local results = table.pack(Server.try(fn, ...))
	span:finish(if results[1] then "ok" else "error")
	if not results[1] then
		error(results[2], 0)
	end
	return table.unpack(results, 2, results.n)
end

---------------------------------------------------------------------------
-- Security
---------------------------------------------------------------------------

--[[
	Records an exploit / anti-cheat detection for a player (Security page).

		Workshell.flag(player, "speed", { speed = 120, max = 32 }, { severity = "high", action = "kicked" })
]]
Server.flag = flag

export type ProtectOptions = {
	rate: number?, -- calls per second allowed per player (default 10)
	burst: number?, -- burst size (default 2 × rate)
	-- Return false to reject the call (and flag the player).
	validate: ((player: Player, ...any) -> boolean)?,
	severity: string?, -- severity of the detections raised (default "medium")
}

--[[
	Connects a RemoteEvent handler with per-player rate limiting and argument
	validation. Abusive calls are dropped and recorded as detections.

		Workshell.protect(remotes.BuyItem, function(player, itemId)
			...
		end, { rate = 5, validate = function(player, itemId) return typeof(itemId) == "string" end })
]]
function Server.protect(remote: RemoteEvent, handler: (Player, ...any) -> (), opts: ProtectOptions?): RBXScriptConnection
	local o = opts or {}
	local rate = o.rate or 10
	local burst = o.burst or rate * 2
	local buckets: { [Player]: { tokens: number, at: number, flagged: number } } = {}
	local remoteName = remote.Name
	Players.PlayerRemoving:Connect(function(player)
		buckets[player] = nil
	end)
	return remote.OnServerEvent:Connect(function(player, ...)
		local now = os.clock()
		local b = buckets[player]
		if not b then
			b = { tokens = burst, at = now, flagged = -math.huge }
			buckets[player] = b
		end
		b.tokens = math.min(burst, b.tokens + (now - b.at) * rate)
		b.at = now
		if b.tokens < 1 then
			if now - b.flagged > 30 then
				b.flagged = now
				flag(player, "remote_spam", { remote = remoteName, limitPerSecond = rate }, { severity = o.severity })
			end
			return
		end
		b.tokens -= 1
		if o.validate then
			local okValidate, valid = pcall(o.validate, player, ...)
			if not okValidate or not valid then
				local args = table.pack(...)
				local types = {}
				for i = 1, math.min(args.n, 8) do
					types[i] = typeof(args[i])
				end
				flag(player, "invalid_remote_args", { remote = remoteName, argTypes = table.concat(types, ",") }, { severity = o.severity })
				return
			end
		end
		local okHandler, err = pcall(handler, player, ...)
		if not okHandler then
			Server.captureError(err, { player = player, context = { remote = remoteName } })
		end
	end)
end

---------------------------------------------------------------------------
-- Metrics
---------------------------------------------------------------------------

local function metric(kind: string, name: string, value: number)
	if not state.enabled or typeof(value) ~= "number" or value ~= value then
		return
	end
	local m = state.metrics[name]
	if not m then
		m = { kind = kind, count = 0, sum = 0, min = value, max = value, last = value }
		state.metrics[name] = m
	end
	m.count += 1
	m.sum += value
	m.min = math.min(m.min, value)
	m.max = math.max(m.max, value)
	m.last = value
end

-- Adds to a counter metric (e.g. "shop.opened"). Summed across servers in the dashboard.
function Server.increment(name: string, amount: number?)
	metric("counter", name, amount or 1)
end

-- Records the current value of a gauge metric (e.g. "match.queue_size").
function Server.gauge(name: string, value: number)
	metric("gauge", name, value)
end

-- Records a duration in milliseconds.
function Server.timing(name: string, ms: number)
	metric("timing", name, ms)
end

-- Runs fn and records how long it took as a timing metric. Returns fn's results.
function Server.time<R...>(name: string, fn: () -> R...): R...
	local start = os.clock()
	local results = table.pack(fn())
	metric("timing", name, (os.clock() - start) * 1000)
	return table.unpack(results, 1, results.n)
end

-- Returns the current session id of a player (or nil).
function Server.sessionId(player: Player): string?
	return state.sessions[player]
end

-- Sends everything queued right now (yields).
function Server.flush()
	flush(true)
end

function Server.isEnabled(): boolean
	return state.enabled
end

return Server
