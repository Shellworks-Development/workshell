-- Minimal Roblox API mocks so the SDK can run under the standalone `luau` CLI.
-- This file is concatenated ahead of the SDK modules by test/run.py.

local clock = 0
local testState = {}
local function warn(...)
	print("WARN", ...)
end
local realTypeof = typeof

local function typeof(v)
	if realTypeof(v) == "table" then
		local mt = getmetatable(v)
		if mt and mt.__type then
			return mt.__type
		end
	end
	return realTypeof(v)
end

-- Signals -------------------------------------------------------------------
local Signal = {}
Signal.__index = Signal
function Signal.new()
	return setmetatable({ handlers = {} }, Signal)
end
function Signal:Connect(fn)
	table.insert(self.handlers, fn)
	return { Disconnect = function() end }
end
function Signal:Fire(...)
	for _, fn in self.handlers do
		local co = coroutine.create(fn)
		local ok, err = coroutine.resume(co, ...)
		if not ok then
			error(err)
		end
	end
end

-- Scheduler -----------------------------------------------------------------
local sleeping = {}
local task = {}
function task.spawn(fn, ...)
	local co = coroutine.create(fn)
	local ok, err = coroutine.resume(co, ...)
	if not ok then
		error(debug.traceback(co, err))
	end
	return co
end
function task.wait(t)
	t = t or 0
	table.insert(sleeping, { co = coroutine.running(), at = clock + t })
	coroutine.yield()
	return t
end
local function advance(seconds)
	local target = clock + seconds
	while true do
		table.sort(sleeping, function(a, b)
			return a.at < b.at
		end)
		local nextItem = sleeping[1]
		if not nextItem or nextItem.at > target then
			break
		end
		table.remove(sleeping, 1)
		clock = nextItem.at
		local ok, err = coroutine.resume(nextItem.co)
		if not ok then
			error(debug.traceback(nextItem.co, err))
		end
	end
	clock = target
end

local os = setmetatable({ clock = function()
	return clock
end }, { __index = os })

local DateTime = {
	now = function()
		return { UnixTimestampMillis = 1790000000000 + math.floor(clock * 1000), UnixTimestamp = 1790000000 + math.floor(clock) }
	end,
}

-- Enums ---------------------------------------------------------------------
local function enumItem(name)
	return setmetatable({ Name = name }, { __type = "EnumItem", __tostring = function()
		return "Enum." .. name
	end })
end
local Enum = {
	HttpContentType = { ApplicationJson = enumItem("HttpContentType.ApplicationJson") },
	MessageType = {
		MessageOutput = enumItem("MessageType.MessageOutput"),
		MessageInfo = enumItem("MessageType.MessageInfo"),
		MessageWarning = enumItem("MessageType.MessageWarning"),
		MessageError = enumItem("MessageType.MessageError"),
	},
	InfoType = { GamePass = enumItem("InfoType.GamePass") },
	HttpCompression = { Gzip = enumItem("HttpCompression.Gzip") },
}

-- Instances -----------------------------------------------------------------
local function instance(className, props)
	local inst = props or {}
	inst.ClassName = className
	inst.Name = inst.Name or className
	inst._children = {}
	function inst:IsA(c)
		return self.ClassName == c
	end
	function inst:GetFullName()
		return (self._path or self.Name)
	end
	function inst:WaitForChild(name)
		while not self._children[name] do
			task.wait(0.1)
		end
		return self._children[name]
	end
	return setmetatable(inst, {
		__type = "Instance",
		__index = function(self, k)
			if k == "Parent" then
				return rawget(self, "_parent")
			end
			return rawget(self, "_children")[k]
		end,
		__newindex = function(self, k, v)
			if k == "Parent" then
				rawset(self, "_parent", v)
				v._children[self.Name] = self
			else
				rawset(self, k, v)
			end
		end,
		__tostring = function(self)
			return self.Name
		end,
	})
end

local Instance = {
	new = function(className)
		local inst = instance(className)
		if className == "RemoteEvent" then
			inst.OnServerEvent = Signal.new()
			inst.FireServer = function(self, ...)
				-- Client → server relay within one process for tests.
				self.OnServerEvent:Fire(testState.localPlayer, ...)
			end
		end
		return inst
	end,
}

-- JSON (enough of Roblox's JSONEncode semantics: {} encodes as []) -----------
local function encode(v)
	local t = realTypeof(v)
	if t == "nil" then
		return "null"
	elseif t == "boolean" then
		return tostring(v)
	elseif t == "number" then
		if v == math.floor(v) and math.abs(v) < 2 ^ 53 then
			return string.format("%d", v)
		end
		return string.format("%.14g", v)
	elseif t == "string" then
		return '"' .. v:gsub('[%c"\\]', function(c)
			local map = { ['"'] = '\\"', ["\\"] = "\\\\", ["\n"] = "\\n", ["\t"] = "\\t", ["\r"] = "\\r" }
			return map[c] or string.format("\\u%04x", string.byte(c))
		end) .. '"'
	elseif t == "table" then
		if getmetatable(v) and getmetatable(v).__type then
			error("cannot encode " .. typeof(v))
		end
		local n = #v
		local count = 0
		for _ in v do
			count += 1
		end
		local parts = {}
		if count == n then
			for i = 1, n do
				parts[i] = encode(v[i])
			end
			return "[" .. table.concat(parts, ",") .. "]"
		end
		for k, x in v do
			if realTypeof(k) ~= "string" then
				error("mixed table keys")
			end
			table.insert(parts, encode(k) .. ":" .. encode(x))
		end
		return "{" .. table.concat(parts, ",") .. "}"
	end
	error("cannot encode " .. t)
end

-- Services ------------------------------------------------------------------
local posts = {}
local guid = 0

local HttpService = {
	GenerateGUID = function()
		guid += 1
		return string.format("00000000-0000-0000-0000-%012d", guid)
	end,
	JSONEncode = function(_, v)
		return encode(v)
	end,
	RequestAsync = function(_, req)
		task.wait(0.05)
		if testState.failNextPost then
			testState.failNextPost = false
			error("HttpError: ConnectFail")
		end
		table.insert(posts, { url = req.Url, body = req.Body, headers = req.Headers, compress = req.Compress == Enum.HttpCompression.Gzip })
		return { Success = true, StatusCode = 202, Body = '{"accepted":1}', Headers = {} }
	end,
	PostAsync = function(_, url, body, contentType, compress, headers)
		task.wait(0.05) -- requests yield
		if testState.failNextPost then
			testState.failNextPost = false
			error("HttpError: ConnectFail")
		end
		table.insert(posts, { url = url, body = body, headers = headers, compress = compress })
		return '{"accepted":1}'
	end,
}

local function newPlayer(userId, name)
	local p = instance("Player", { UserId = userId, Name = name, DisplayName = name, AccountAge = 30, MembershipType = { Name = "None" } })
	p.GetNetworkPing = function()
		return 0.04
	end
	return p
end

local playerList = {}
local Players = {
	MaxPlayers = 20,
	PlayerAdded = Signal.new(),
	PlayerRemoving = Signal.new(),
	GetPlayers = function()
		return table.clone(playerList)
	end,
}

local services = {
	HttpService = HttpService,
	LocalizationService = {
		GetCountryRegionForPlayerAsync = function()
			task.wait(0.2)
			return "AU"
		end,
	},
	LogService = { MessageOut = Signal.new() },
	MarketplaceService = {
		PromptGamePassPurchaseFinished = Signal.new(),
		GetProductInfo = function()
			return { Name = "VIP", PriceInRobux = 199 }
		end,
	},
	Players = Players,
	RunService = {
		IsStudio = function()
			return false
		end,
		IsServer = function()
			return true
		end,
		IsClient = function()
			return false
		end,
		Heartbeat = Signal.new(),
		RenderStepped = Signal.new(),
	},
	ServerStorage = { FindFirstChild = function() return nil end },
	ReplicatedStorage = { FindFirstChild = function() return nil end },
	ServerScriptService = { FindFirstChild = function() return nil end },
	ScriptContext = { Error = Signal.new() },
	Stats = {
		HeartbeatTimeMs = 2.5,
		InstanceCount = 15000,
		DataSendKbps = 40,
		DataReceiveKbps = 5,
		GetTotalMemoryUsageMb = function()
			return 900
		end,
	},
	GuiService = { IsTenFootInterface = function()
		return false
	end },
	UserInputService = { TouchEnabled = false, KeyboardEnabled = true },
	VRService = { VREnabled = false },
}

local closeHandlers = {}
local game = {
	JobId = "test-job-0001",
	PlaceId = 1234,
	PlaceVersion = 77,
	PrivateServerId = "",
	PrivateServerOwnerId = 0,
	GetService = function(_, name)
		return assert(services[name], "no mock for service " .. name)
	end,
	BindToClose = function(_, fn)
		table.insert(closeHandlers, fn)
	end,
}
local workspace = { CurrentCamera = nil }
