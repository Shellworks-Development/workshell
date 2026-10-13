--!strict
-- Helpers shared by the server and client halves of the SDK.

local Shared = {}

Shared.VERSION = "0.2.0"
Shared.REMOTE_NAME = "WorkshellRemote"
Shared.LOG_PREFIX = "[Workshell]"

function Shared.now(): number
	return DateTime.now().UnixTimestampMillis
end

function Shared.truncate(s: string, max: number): string
	if #s <= max then
		return s
	end
	-- Avoid cutting a multi-byte UTF-8 sequence in half.
	local cut = max
	while cut > 0 do
		local byte = string.byte(s, cut + 1)
		if byte == nil or byte < 0x80 or byte >= 0xC0 then
			break
		end
		cut -= 1
	end
	return string.sub(s, 1, cut) .. "…"
end

local function isArray(t: { [any]: any }): boolean
	local n = #t
	local count = 0
	for _ in t do
		count += 1
		if count > n then
			return false
		end
	end
	return count == n
end

--[[
	Converts an arbitrary Luau value into something HttpService:JSONEncode
	accepts: Instances become their full name, Roblox datatypes become strings,
	cycles and deep nesting are cut off, and NaN/inf become nil.
]]
function Shared.sanitize(value: any, depth: number?): any
	local d = depth or 0
	local t = typeof(value)
	if t == "nil" or t == "boolean" then
		return value
	elseif t == "number" then
		if value ~= value or value == math.huge or value == -math.huge then
			return nil
		end
		return value
	elseif t == "string" then
		return Shared.truncate(value, 1000)
	elseif t == "Instance" then
		local ok, name = pcall(function()
			return value:GetFullName()
		end)
		return if ok then name else tostring(value)
	elseif t == "EnumItem" then
		return tostring(value)
	elseif t == "table" then
		if d >= 4 then
			return "<table>"
		end
		local out = {}
		if isArray(value) then
			for i, v in value do
				if i > 50 then
					break
				end
				out[i] = Shared.sanitize(v, d + 1)
			end
		else
			local count = 0
			for k, v in value do
				count += 1
				if count > 50 then
					break
				end
				out[Shared.truncate(tostring(k), 64)] = Shared.sanitize(v, d + 1)
			end
		end
		return out
	end
	return tostring(value)
end

-- Sanitizes a dictionary, returning nil when empty (Roblox encodes {} as [],
-- so empty dictionaries are omitted instead).
function Shared.dict(t: { [any]: any }?): { [string]: any }?
	if t == nil then
		return nil
	end
	local out = Shared.sanitize(t)
	if typeof(out) ~= "table" or next(out) == nil then
		return nil
	end
	return out
end

return Shared
