--!nonstrict
-- Client half of the Workshell SDK. Clients can't make HTTP requests, so
-- errors and events are relayed through the server, which rate-limits them.
-- Require the Workshell module from any LocalScript to enable this.

local GuiService = game:GetService("GuiService")
local LogService = game:GetService("LogService")
local Players = game:GetService("Players")
local RunService = game:GetService("RunService")
local ScriptContext = game:GetService("ScriptContext")
local Stats = game:GetService("Stats")
local UserInputService = game:GetService("UserInputService")
local VRService = game:GetService("VRService")

local Shared = require(script.Parent.Shared)

local Client = {}

local remote: RemoteEvent? = nil
local pending: { { any } } = {}
local breadcrumbs: { any } = {}
local seenErrors: { [string]: number } = {}

local function detectPlatform(): string
	if VRService.VREnabled then
		return "VR"
	elseif GuiService:IsTenFootInterface() then
		return "Console"
	elseif UserInputService.TouchEnabled and not UserInputService.KeyboardEnabled then
		return if workspace.CurrentCamera and workspace.CurrentCamera.ViewportSize.X >= 1000 then "Tablet" else "Phone"
	end
	return "Desktop"
end

local platform = detectPlatform()

local function send(kind: string, payload: { [string]: any })
	if remote then
		remote:FireServer(kind, payload)
	elseif #pending < 50 then
		table.insert(pending, { kind, payload })
	end
end

local function addBreadcrumb(category: string, message: string, level: string?)
	table.insert(breadcrumbs, { ts = Shared.now(), category = category, message = Shared.truncate(message, 200), level = level })
	if #breadcrumbs > 20 then
		table.remove(breadcrumbs, 1)
	end
end

local function report(message: string, stack: string, scriptName: string)
	-- The server rate-limits too, but don't spam the remote with an error
	-- that's thrown every frame: send each distinct error at most every 10s.
	local key = message .. "\0" .. stack
	local now = os.clock()
	if seenErrors[key] and now - seenErrors[key] < 10 then
		return
	end
	seenErrors[key] = now
	send("error", {
		message = Shared.truncate(message, 2000),
		stack = Shared.truncate(stack, 4000),
		script = scriptName,
		platform = platform,
		breadcrumbs = table.clone(breadcrumbs),
	})
end

local started = false
local function start()
	if started then
		return
	end
	started = true

	ScriptContext.Error:Connect(function(message, stack, origin)
		local name = ""
		if origin then
			local ok, full = pcall(origin.GetFullName, origin)
			name = if ok then full else tostring(origin)
		end
		report(message, stack, name)
	end)
	LogService.MessageOut:Connect(function(message, messageType)
		if messageType == Enum.MessageType.MessageWarning then
			addBreadcrumb("output", message, "warn")
		elseif messageType == Enum.MessageType.MessageError then
			addBreadcrumb("output", message, "error")
		end
	end)

	task.spawn(function()
		local found = script.Parent:WaitForChild(Shared.REMOTE_NAME, 60)
		if not found then
			return -- server SDK not initialized (or client reporting disabled)
		end
		remote = found
		found:FireServer("device", { platform = platform })
		for _, p in pending do
			found:FireServer(p[1], p[2])
		end
		table.clear(pending)
	end)

	-- Client performance: average FPS, memory and ping every 30 seconds.
	task.spawn(function()
		local frames, elapsed = 0, 0
		RunService.RenderStepped:Connect(function(dt)
			frames += 1
			elapsed += dt
		end)
		while true do
			task.wait(30)
			if remote and elapsed > 0 then
				local okPing, ping = pcall(function()
					return Players.LocalPlayer:GetNetworkPing() * 2000
				end)
				send("perf", {
					fps = math.floor(frames / elapsed * 10 + 0.5) / 10,
					memoryMb = Stats:GetTotalMemoryUsageMb(),
					pingMs = if okPing then ping else nil,
					platform = platform,
				})
			end
			frames, elapsed = 0, 0
		end
	end)
end

-- Client errors are captured automatically once the module is required.
start()

-- Reports a handled error from the client.
function Client.captureError(err: any, opts: { stack: string? }?)
	report(tostring(err), (opts and opts.stack) or debug.traceback(nil, 2), "")
end

-- Tracks an analytics event for the local player. Client events are marked
-- with `$client = true`, since clients can forge them.
function Client.track(name: string, props: { [string]: any }?, value: number?)
	addBreadcrumb("event", name)
	send("event", { name = name, props = Shared.dict(props), value = value })
end

function Client.addBreadcrumb(message: string, category: string?)
	addBreadcrumb(category or "custom", message)
end

-- Like Server.try: calls fn and reports any error.
function Client.try(fn: (...any) -> ...any, ...: any): (boolean, ...any)
	local stack
	local results = table.pack(xpcall(fn, function(e)
		stack = debug.traceback(nil, 2)
		return e
	end, ...))
	if not results[1] then
		Client.captureError(results[2], { stack = stack })
	end
	return table.unpack(results, 1, results.n)
end

function Client.platform(): string
	return platform
end

-- Server-only functions are harmless no-ops on the client so shared modules
-- can call them without checking.
local function noop() end
Client.init = noop
Client.log = noop
Client.debug = noop
Client.info = noop
Client.warn = noop
Client.error = noop
Client.trackPurchase = noop
Client.increment = noop
Client.gauge = noop
Client.timing = noop
Client.setTag = noop
Client.setContext = noop
Client.setUser = noop
Client.flag = noop -- detections must come from the server, which clients can't forge
Client.protect = noop
Client.flush = noop

-- Spans are server-side; on the client these just run the function.
function Client.startSpan(_name: string)
	return { setAttribute = noop, finish = noop, child = function(self)
		return self
	end }
end

function Client.trace(_name: string, fn: (...any) -> ...any, _player: any, ...: any): ...any
	return fn(...)
end

function Client.sessionId(): string?
	return nil
end

function Client.time<R...>(_name: string, fn: () -> R...): R...
	return fn()
end

function Client.wrap<A..., R...>(fn: (A...) -> R...): (A...) -> R...
	return function(...)
		local results = table.pack(Client.try(fn, ...))
		if not results[1] then
			error(results[2], 0)
		end
		return table.unpack(results, 2, results.n)
	end :: any
end

function Client.isEnabled(): boolean
	return remote ~= nil
end

return Client
