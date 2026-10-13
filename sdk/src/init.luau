--[[
	Workshell — error tracking, logging, analytics and monitoring for Roblox.

	Server (a Script in ServerScriptService):

		local Workshell = require(game.ReplicatedStorage.Workshell)
		Workshell.init({
			url = "https://workshell.example.com",
			key = "wsk_...",
		})

	Client (optional, any LocalScript) — enables client error reporting:

		local Workshell = require(game.ReplicatedStorage.Workshell)

	See the Workshell README for the full API.
]]

local RunService = game:GetService("RunService")

if RunService:IsServer() then
	return require(script.Server)
end
return require(script.Client)
