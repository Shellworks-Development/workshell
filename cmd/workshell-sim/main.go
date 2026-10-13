// Command workshell-sim sends realistic fake telemetry to a Workshell server,
// as if several Roblox game servers had been running for the past few days.
// It's for trying out the dashboard and for development.
//
//	go run ./cmd/workshell-sim -url http://localhost:8080 -key wsk_...
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"time"
)

type item map[string]any

type client struct {
	url, key string
	http     *http.Client
	sent     int
}

func (c *client) send(server map[string]any, items []item) {
	for len(items) > 0 {
		n := min(len(items), 2000)
		body, _ := json.Marshal(map[string]any{
			"sdk":    map[string]any{"name": "workshell-sim", "version": "0.1.0"},
			"server": server,
			"items":  items[:n],
		})
		var gz bytes.Buffer
		w := gzip.NewWriter(&gz)
		w.Write(body)
		w.Close()
		for attempt := 0; ; attempt++ {
			req, _ := http.NewRequest(http.MethodPost, c.url+"/api/v1/ingest", bytes.NewReader(gz.Bytes()))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Content-Encoding", "gzip")
			req.Header.Set("X-Workshell-Key", c.key)
			resp, err := c.http.Do(req)
			if err != nil {
				log.Fatalf("send: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode < 300 {
				break
			}
			// Rate limited or queue full: back off like the SDK does.
			if (resp.StatusCode == 429 || resp.StatusCode == 503) && attempt < 120 {
				if attempt == 0 {
					log.Printf("server asked us to slow down (HTTP %d); raise the project's ingest limit for faster backfills", resp.StatusCode)
				}
				time.Sleep(time.Second)
				continue
			}
			log.Fatalf("send: HTTP %d", resp.StatusCode)
		}
		c.sent += n
		items = items[n:]
	}
}

func (c *client) register(release map[string]any) {
	body, _ := json.Marshal(release)
	req, _ := http.NewRequest(http.MethodPost, c.url+"/api/v1/releases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workshell-Key", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		log.Fatalf("register release: %v", err)
	}
	resp.Body.Close()
}

// simSourcemap is a `rojo sourcemap`-shaped tree for the simulated scripts.
var simSourcemap = map[string]any{"name": "Dungeon Quest", "className": "DataModel", "children": []any{
	map[string]any{"name": "ServerScriptService", "className": "ServerScriptService", "children": []any{
		map[string]any{"name": "Combat", "className": "Folder", "children": []any{
			map[string]any{"name": "WeaponHandler", "className": "Script", "filePaths": []string{"src/server/Combat/WeaponHandler.server.luau"}},
		}},
		map[string]any{"name": "Data", "className": "Folder", "children": []any{
			map[string]any{"name": "PlayerStore", "className": "ModuleScript", "filePaths": []string{"src/server/Data/PlayerStore.luau"}},
		}},
		map[string]any{"name": "Matchmaking", "className": "Script", "filePaths": []string{"src/server/Matchmaking.server.luau"}},
		map[string]any{"name": "QuestService", "className": "Script", "filePaths": []string{"src/server/QuestService.server.luau"}},
		map[string]any{"name": "Pets", "className": "Folder", "children": []any{
			map[string]any{"name": "PetFollow", "className": "Script", "filePaths": []string{"src/server/Pets/PetFollow.server.luau"}},
		}},
	}},
	map[string]any{"name": "ReplicatedStorage", "className": "ReplicatedStorage", "children": []any{
		map[string]any{"name": "Modules", "className": "Folder", "children": []any{
			map[string]any{"name": "Quest", "className": "ModuleScript", "filePaths": []string{"src/shared/Modules/Quest.luau"}},
		}},
	}},
	map[string]any{"name": "StarterGui", "className": "StarterGui", "children": []any{
		map[string]any{"name": "ShopUI", "className": "ScreenGui", "children": []any{
			map[string]any{"name": "ShopController", "className": "LocalScript", "filePaths": []string{"src/client/ShopUI/ShopController.client.luau"}},
		}},
	}},
	map[string]any{"name": "StarterPlayer", "className": "StarterPlayer", "children": []any{
		map[string]any{"name": "StarterPlayerScripts", "className": "StarterPlayerScripts", "children": []any{
			map[string]any{"name": "CameraController", "className": "LocalScript", "filePaths": []string{"src/client/CameraController.client.luau"}},
		}},
	}},
}}

type errorKind struct {
	message, stack string
	side           string
	weight         float64 // relative frequency
	sinceDay       float64 // first appears this many days ago (introduced by a release)
}

var errorKinds = []errorKind{
	{"ServerScriptService.Combat.WeaponHandler:142: attempt to index nil with 'Ammo'", "ServerScriptService.Combat.WeaponHandler, line 142 - function fire\nServerScriptService.Combat.WeaponHandler, line 88 - function onActivated\n", "server", 6, 7},
	{"ServerScriptService.Data.PlayerStore:57: DataStore request was added to queue. If request queue fills, further requests will be dropped. Try sending fewer requests.Key = Player_%d", "ServerScriptService.Data.PlayerStore, line 57 - function save\nServerScriptService.Data.PlayerStore, line 112\n", "server", 3, 7},
	{"ServerScriptService.Matchmaking:33: TeleportService:TeleportAsync failed: Unable to join game instance", "ServerScriptService.Matchmaking, line 33 - function sendToMatch\n", "server", 1.5, 5},
	{"Players.Player.PlayerGui.ShopUI.ShopController:210: attempt to perform arithmetic (sub) on nil and number", "Players.Player.PlayerGui.ShopUI.ShopController, line 210 - function updatePrice\nPlayers.Player.PlayerGui.ShopUI.ShopController, line 64\n", "client", 4, 3},
	{"ReplicatedStorage.Modules.Quest:91: Quest 'daily_%d' not found", "ReplicatedStorage.Modules.Quest, line 91 - function getQuest\nServerScriptService.QuestService, line 40 - function claim\n", "server", 2, 7},
	{"Players.Player.PlayerScripts.CameraController:18: attempt to index nil with 'Humanoid'", "Players.Player.PlayerScripts.CameraController, line 18 - function onCharacterAdded\n", "client", 5, 7},
	{"ServerScriptService.Pets.PetFollow:77: Script timeout: exhausted allowed execution time", "ServerScriptService.Pets.PetFollow, line 77 - function step\n", "server", 0.6, 1},
}

var platforms = []string{"Desktop", "Desktop", "Phone", "Phone", "Phone", "Tablet", "Console"}
var countries = []string{"US", "US", "US", "BR", "GB", "PH", "DE", "MX", "CA", "AU", "ID", "FR"}

func main() {
	url := flag.String("url", "http://localhost:8080", "Workshell server URL")
	key := flag.String("key", os.Getenv("WORKSHELL_KEY"), "project ingest key")
	days := flag.Float64("days", 6.5, "days of history to generate (max ~7, older data is rejected)")
	servers := flag.Int("servers", 6, "concurrent game servers")
	live := flag.Bool("live", true, "leave servers live (they'll show as live for ~90s after the run)")
	flag.Parse()
	if *key == "" {
		log.Fatal("-key is required")
	}
	c := &client{url: *url, key: *key, http: &http.Client{Timeout: 60 * time.Second}}

	now := time.Now()
	start := now.Add(-time.Duration(*days * float64(24*time.Hour)))
	r := rand.New(rand.NewPCG(1, 2))

	// Player pool: a stable base of returning players plus a stream of new ones.
	var nextUser int64 = 100_000_000
	type player struct {
		id       int64
		loyalty  float64 // chance of coming back each day
		platform string
		country  string
		spender  bool
	}
	var pool []*player
	newPlayer := func() *player {
		nextUser += int64(r.IntN(5000) + 1)
		p := &player{
			id:       nextUser,
			loyalty:  math.Pow(r.Float64(), 1.8),
			platform: platforms[r.IntN(len(platforms))],
			country:  countries[r.IntN(len(countries))],
			spender:  r.Float64() < 0.06,
		}
		pool = append(pool, p)
		return p
	}

	// Servers live ~6h, then are replaced (with the latest place version).
	type gameServer struct {
		job     string
		started time.Time
		ends    time.Time
		version int
		release string
		players map[int64]time.Time // userId → session end
		session map[int64]string
	}
	placeVersion := func(t time.Time) int { return 100 + int(t.Sub(start).Hours()/30) }
	releaseOf := func(pv int) string {
		h := uint64(pv) * 0x9E3779B97F4A7C15
		return fmt.Sprintf("%07x", h>>36)
	}
	commitMessages := []string{"Fix ammo desync in weapon handler", "Add daily quests", "Shop UI refresh", "Matchmaking: retry teleports", "Pets follow smoothing", "Datastore retry backoff", "Season 2 map"}
	authors := []string{"Ava", "Noah", "Mia", "Liam"}
	for pv := placeVersion(start); pv <= placeVersion(now); pv++ {
		c.register(map[string]any{
			"version": releaseOf(pv), "commit": releaseOf(pv) + "e1d2c3b4a5968778695a4b3c2d1e0f9a8b7c", "branch": "main",
			"message": commitMessages[pv%len(commitMessages)], "author": authors[pv%len(authors)], "placeVersion": fmt.Sprint(pv),
			"sourcemap": simSourcemap,
		})
	}
	jobN := 0
	newServer := func(t time.Time) *gameServer {
		jobN++
		return &gameServer{
			job:     fmt.Sprintf("%08x-%04x-4%03x-a%03x-%012x", r.Uint32(), jobN, r.IntN(4096), r.IntN(4096), r.Uint64()&0xffffffffffff),
			started: t,
			ends:    t.Add(time.Duration(3+r.IntN(6)) * time.Hour),
			version: placeVersion(t),
			release: releaseOf(placeVersion(t)),
			players: map[int64]time.Time{},
			session: map[int64]string{},
		}
	}
	var fleet []*gameServer
	for range *servers {
		fleet = append(fleet, newServer(start))
	}

	pending := map[*gameServer][]item{}
	info := func(s *gameServer) map[string]any {
		return map[string]any{"jobId": s.job, "placeId": 1818, "placeVersion": s.version, "release": s.release, "serverType": "public", "environment": "production", "maxPlayers": 24}
	}
	flushServer := func(s *gameServer) {
		if len(pending[s]) > 0 {
			c.send(info(s), pending[s])
			pending[s] = nil
		}
	}
	add := func(s *gameServer, it item, t time.Time) {
		it["ts"] = t.UnixMilli()
		pending[s] = append(pending[s], it)
		if len(pending[s]) >= 2000 {
			flushServer(s)
		}
	}

	step := 30 * time.Second
	sessionSeq := 0
	for t := start; t.Before(now); t = t.Add(step) {
		hour := float64(t.UTC().Hour()) + float64(t.UTC().Minute())/60
		// Daily cycle peaking around 20:00 UTC, weekend boost, and gentle growth.
		diurnal := 0.55 + 0.45*math.Sin((hour-14)/24*2*math.Pi)
		growth := 1 + 0.06*t.Sub(start).Hours()/24
		weekend := 1.0
		if wd := t.UTC().Weekday(); wd == time.Saturday || wd == time.Sunday {
			weekend = 1.25
		}
		target := 9 * diurnal * growth * weekend // players per server

		for i, s := range fleet {
			if t.After(s.ends) {
				for uid := range s.players {
					add(s, item{"type": "session_end", "sessionId": s.session[uid], "userId": uid}, t)
				}
				add(s, item{"type": "server_close"}, t)
				flushServer(s)
				fleet[i] = newServer(t)
				s = fleet[i]
				add(s, item{"type": "log", "level": "info", "source": "workshell", "message": "Server started"}, t)
			}

			// Leaves
			for uid, end := range s.players {
				if t.After(end) {
					add(s, item{"type": "session_end", "sessionId": s.session[uid], "userId": uid}, t)
					add(s, item{"type": "log", "level": "info", "source": "output", "message": fmt.Sprintf("Player %d left", uid)}, t)
					delete(s.players, uid)
				}
			}
			// Joins
			for float64(len(s.players)) < target && r.Float64() < 0.35 && len(s.players) < 24 {
				var p *player
				if len(pool) > 30 && r.Float64() < 0.8 {
					p = pool[r.IntN(len(pool))]
					if r.Float64() > p.loyalty+0.05 {
						continue
					}
				} else {
					p = newPlayer()
				}
				if _, already := s.players[p.id]; already {
					continue
				}
				sessionSeq++
				sid := fmt.Sprintf("sess-%d", sessionSeq)
				length := time.Duration(math.Min(r.ExpFloat64()*14+2, 180) * float64(time.Minute))
				s.players[p.id] = t.Add(length)
				s.session[p.id] = sid
				add(s, item{"type": "session_start", "sessionId": sid, "userId": p.id, "country": p.country,
					"attrs": map[string]any{"membership": []string{"None", "Premium"}[p.id%2], "accountAge": 30 + p.id%900}}, t)
				loadMs := 40 + r.ExpFloat64()*90
				if r.Float64() < 0.02 {
					loadMs += 2000 + r.Float64()*3000
				}
				status := "ok"
				if r.Float64() < 0.01 {
					status = "error"
				}
				add(s, item{"type": "span", "name": "datastore.load", "durationMs": loadMs, "status": status, "userId": p.id, "sessionId": sid}, t.Add(time.Second))
				add(s, item{"type": "span", "name": "datastore.save", "durationMs": 30 + r.ExpFloat64()*60, "status": "ok", "userId": p.id, "sessionId": sid}, t.Add(length))
				for pt := time.Duration(0); pt < length; pt += 2 * time.Minute {
					base := map[string]float64{"Desktop": 120, "Phone": 48, "Tablet": 55, "Console": 60}[p.platform]
					add(s, item{"type": "client_perf", "sessionId": sid, "platform": p.platform, "fps": max(8, base-r.Float64()*18-float64(len(s.players))),
						"pingMs": 40 + r.Float64()*120, "memoryMb": 900 + r.Float64()*900}, t.Add(pt+30*time.Second))
				}
				if r.Float64() < 0.012 {
					kinds := []struct{ k, sev, action string }{{"speed", "high", "kicked"}, {"teleport", "high", "kicked"}, {"fly", "medium", ""}, {"remote_spam", "low", ""}, {"invalid_remote_args", "medium", ""}}
					d := kinds[r.IntN(len(kinds))]
					add(s, item{"type": "detection", "userId": p.id, "sessionId": sid, "kind": d.k, "severity": d.sev, "action": d.action,
						"details": map[string]any{"observed": 40 + r.IntN(200), "limit": 32}}, t.Add(time.Duration(r.IntN(int(length.Seconds())+1))*time.Second))
				}
				add(s, item{"type": "session_update", "sessionId": sid, "platform": p.platform}, t.Add(2*time.Second))
				add(s, item{"type": "log", "level": "info", "source": "output", "message": fmt.Sprintf("Player %d joined", p.id)}, t)

				// Gameplay events over the session
				level := 1 + r.IntN(30)
				sess := func(it item) item { it["sessionId"] = sid; return it }
				for e := time.Duration(0); e < length; e += time.Duration(2+r.IntN(6)) * time.Minute {
					et := t.Add(e)
					if et.After(now) {
						break
					}
					switch x := r.Float64(); {
					case x < 0.35:
						level++
						add(s, sess(item{"type": "event", "name": "level_up", "userId": p.id, "props": map[string]any{"level": level, "class": []string{"warrior", "mage", "ranger"}[p.id%3]}}), et)
					case x < 0.55:
						add(s, sess(item{"type": "event", "name": "quest_completed", "userId": p.id, "props": map[string]any{"quest": fmt.Sprintf("daily_%d", r.IntN(6))}}), et)
					case x < 0.75:
						add(s, sess(item{"type": "event", "name": "shop_opened", "userId": p.id}), et)
						if p.spender && r.Float64() < 0.3 {
							price := []float64{25, 49, 99, 199, 499}[r.IntN(5)]
							add(s, sess(item{"type": "event", "name": "$purchase", "userId": p.id, "value": price, "props": map[string]any{"kind": "product", "productId": 1000 + int(price), "productName": fmt.Sprintf("%d Gems", int(price)*10)}}), et.Add(20*time.Second))
						}
					default:
						add(s, sess(item{"type": "event", "name": "coins_earned", "userId": p.id, "value": float64(10 + r.IntN(200)), "props": map[string]any{"source": []string{"quest", "pvp", "chest"}[r.IntN(3)]}}), et)
					}
				}
			}

			// Heartbeat
			n := len(s.players)
			fps := 59.5 - float64(n)*0.15 - r.Float64()*1.5
			if r.Float64() < 0.01 {
				fps -= 20 + r.Float64()*15
			}
			uptime := t.Sub(s.started).Hours()
			add(s, item{
				"type": "heartbeat", "players": n, "fps": fps,
				"heartbeatMs": 1000/fps - 14 + r.Float64()*2,
				"memoryMb":    780 + float64(n)*22 + uptime*35 + r.Float64()*20,
				"pingMs":      70 + r.Float64()*60, "instances": 18000 + n*900 + r.IntN(500),
				"sendKbps": 20 + float64(n)*6 + r.Float64()*10, "recvKbps": 3 + float64(n)*0.8,
			}, t)

			// Errors
			for _, k := range errorKinds {
				if t.Before(now.Add(-time.Duration(k.sinceDay * float64(24*time.Hour)))) {
					continue
				}
				if r.Float64() < k.weight*0.0025*float64(max(n, 1))/6 {
					msg := k.message
					if bytes.Contains([]byte(msg), []byte("%d")) {
						msg = fmt.Sprintf(msg, r.IntN(999999))
					}
					var uid any
					var sessionID string
					if n > 0 && (k.side == "client" || r.Float64() < 0.6) {
						for id := range s.players {
							uid = id
							sessionID = s.session[id]
							break
						}
					}
					add(s, item{
						"type": "error", "side": k.side, "message": msg, "stack": k.stack, "userId": uid, "sessionId": sessionID,
						"breadcrumbs": []any{
							map[string]any{"ts": t.Add(-40 * time.Second).UnixMilli(), "category": "event", "message": "shop_opened"},
							map[string]any{"ts": t.Add(-5 * time.Second).UnixMilli(), "category": "output", "message": "Loading weapon config", "level": "info"},
						},
						"tags": map[string]any{"region": "us-east"},
					}, t)
					add(s, item{"type": "log", "level": "error", "source": "output", "message": msg}, t)
				}
			}
			if r.Float64() < 0.004 {
				add(s, item{"type": "log", "level": "warn", "source": "sdk", "message": "DataStore budget low", "attrs": map[string]any{"budget": r.IntN(20), "requestType": "SetIncrementAsync"}}, t)
			}

			// Custom metrics, aggregated per minute like the SDK does
			if t.Second() < 30 {
				add(s, item{"type": "span", "name": "matchmaking.tick", "durationMs": 2 + r.ExpFloat64()*6 + float64(n)*0.3, "status": "ok"}, t)
				opened := r.IntN(n + 1)
				if opened > 0 {
					add(s, item{"type": "metric", "name": "shop.opened", "kind": "counter", "count": opened, "sum": float64(opened), "min": 1.0, "max": 1.0, "last": 1.0}, t)
				}
				lat := 40 + r.Float64()*80
				add(s, item{"type": "metric", "name": "datastore.load", "kind": "timing", "count": 4, "sum": lat * 4, "min": lat * 0.6, "max": lat * 2.2, "last": lat}, t)
				q := float64(r.IntN(12))
				add(s, item{"type": "metric", "name": "matchmaking.queue", "kind": "gauge", "count": 1, "sum": q, "min": q, "max": q, "last": q}, t)
			}
		}
	}
	for _, s := range fleet {
		if !*live {
			add(s, item{"type": "server_close"}, now)
		}
		flushServer(s)
	}
	fmt.Printf("sent %d items for %d players across %d servers\n", c.sent, len(pool), jobN)
}
