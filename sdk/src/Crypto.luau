--!strict
--!native
-- SHA-256 and HMAC-SHA256 for signing ingest requests.

local Crypto = {}

local K = {
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

local band, bor, bxor, bnot, rrotate, rshift = bit32.band, bit32.bor, bit32.bxor, bit32.bnot, bit32.rrotate, bit32.rshift

-- Returns the 32-byte SHA-256 digest of s as a binary string.
function Crypto.sha256(s: string): string
	local len = #s
	-- Pad: 0x80, zeros, then 64-bit big-endian bit length; total multiple of 64.
	local padded = (len + 9 + 63) // 64 * 64
	local b = buffer.create(padded)
	buffer.writestring(b, 0, s)
	buffer.writeu8(b, len, 0x80)
	local bits = len * 8
	buffer.writeu32(b, padded - 4, bit32.byteswap(bits % 0x100000000))
	buffer.writeu32(b, padded - 8, bit32.byteswap(bits // 0x100000000))

	local h0, h1, h2, h3 = 0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a
	local h4, h5, h6, h7 = 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19
	local w = table.create(64, 0)

	for chunk = 0, padded - 1, 64 do
		for i = 0, 15 do
			w[i + 1] = bit32.byteswap(buffer.readu32(b, chunk + i * 4))
		end
		for i = 17, 64 do
			local w15, w2 = w[i - 15], w[i - 2]
			local s0 = bxor(rrotate(w15, 7), rrotate(w15, 18), rshift(w15, 3))
			local s1 = bxor(rrotate(w2, 17), rrotate(w2, 19), rshift(w2, 10))
			w[i] = (w[i - 16] + s0 + w[i - 7] + s1) % 0x100000000
		end
		local a, bb, c, d, e, f, g, h = h0, h1, h2, h3, h4, h5, h6, h7
		for i = 1, 64 do
			local S1 = bxor(rrotate(e, 6), rrotate(e, 11), rrotate(e, 25))
			local ch = bxor(band(e, f), band(bnot(e), g))
			local t1 = (h + S1 + ch + K[i] + w[i]) % 0x100000000
			local S0 = bxor(rrotate(a, 2), rrotate(a, 13), rrotate(a, 22))
			local maj = bxor(band(a, bb), band(a, c), band(bb, c))
			local t2 = (S0 + maj) % 0x100000000
			h, g, f, e = g, f, e, (d + t1) % 0x100000000
			d, c, bb, a = c, bb, a, (t1 + t2) % 0x100000000
		end
		h0 = (h0 + a) % 0x100000000
		h1 = (h1 + bb) % 0x100000000
		h2 = (h2 + c) % 0x100000000
		h3 = (h3 + d) % 0x100000000
		h4 = (h4 + e) % 0x100000000
		h5 = (h5 + f) % 0x100000000
		h6 = (h6 + g) % 0x100000000
		h7 = (h7 + h) % 0x100000000
	end

	local out = buffer.create(32)
	for i, v in { h0, h1, h2, h3, h4, h5, h6, h7 } do
		buffer.writeu32(out, (i - 1) * 4, bit32.byteswap(v))
	end
	return buffer.tostring(out)
end

function Crypto.toHex(s: string): string
	return (string.gsub(s, ".", function(c)
		return string.format("%02x", string.byte(c))
	end))
end

-- HMAC-SHA256(key, message) as lowercase hex.
function Crypto.hmacSha256Hex(key: string, message: string): string
	if #key > 64 then
		key = Crypto.sha256(key)
	end
	key ..= string.rep("\0", 64 - #key)
	local ipad = string.gsub(key, ".", function(c)
		return string.char(bxor(string.byte(c), 0x36))
	end)
	local opad = string.gsub(key, ".", function(c)
		return string.char(bxor(string.byte(c), 0x5c))
	end)
	return Crypto.toHex(Crypto.sha256(opad .. Crypto.sha256(ipad .. message)))
end

return Crypto
