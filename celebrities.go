// Each player provides their username, and the name of a celebrity or famous figure, alive or dead, fictional or real
// These are combined into a list, and provided to a moderator
// This list of celebrities (but not who provided each name) is read off by the moderator, or displayed briefly to all connected players
// Then, players take turns trying to guess which player provided each name
// If a guess is correct, the player whose character was guessed "loses", and joins the team of the guessing player
// Then, they get another chance to guess
// If a guess is incorrect, the turn passes to the next player

// Display formats:
// Tiles of each player name and celebrity name, dragged to match
// Two text fields, one for player name and one for celebrity name

// Implementation details:
// - Use websockets to send updates from all joined players to each new player
// - Identify players by cookie on first connection

// How to play
// - Each player joins, is assigned a cookie, and prompted for their name and a celebrity name
// - Alternatively, they can choose to be the moderator, if one does not already exist
// - Order player turns by who provided their celebrity name first
// - Information is provided in two columns: player names, and celebrity names (not ordered/sorted)

package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
)

// Player holds the data we store server-side
type Player struct {
	PlayerID  string
	Username  string
	Celebrity string
}

// Messages coming from clients
type JoinMessage struct {
	Type      string `json:"type"`      // "join"
	Username  string `json:"username"`  // player's username
	Celebrity string `json:"celebrity"` // celebrity name
}

// Messages sent to clients
type CelebrityListMessage struct {
	Type        string   `json:"type"`        // "celebrity_list"
	Celebrities []string `json:"celebrities"` // list of celebrity names
}

// Sent to a single client when there's a username/celebrity collision
type CollisionMessage struct {
	Type    string `json:"type"`    // "collision"
	Field   string `json:"field"`   // "username" or "celebrity"
	Message string `json:"message"` // user-facing text
}

type Client struct {
	conn     *websocket.Conn
	send     chan interface{}
	playerID string
}

type joinRequest struct {
	client *Client
	msg    JoinMessage
}

type Hub struct {
	clients    map[*Client]bool
	players    []Player
	register   chan *Client
	unregister chan *Client
	joins      chan joinRequest

	mu sync.RWMutex
}

func newHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		joins:      make(chan joinRequest),
	}
}

func randomGameID(n int) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const max = byte(255 - (256 % len(letters)))

	out := make([]byte, 0, n)
	buf := make([]byte, n*2)

	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}

		for _, b := range buf {
			if b <= max {
				out = append(out, letters[int(b)%len(letters)])
				if len(out) == n {
					return string(out)
				}
			}
		}
	}

	return string(out)
}

func (h *Hub) run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			celebs := h.currentCelebritiesLocked()
			h.mu.Unlock()

			c.send <- CelebrityListMessage{
				Type:        "celebrity_list",
				Celebrities: celebs,
			}

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			playerID := c.playerID
			h.mu.Unlock()

			if playerID != "" {
				go h.scheduleRemoval(playerID, 10*time.Second)
			}

		case jr := <-h.joins:
			j := jr.msg
			c := jr.client

			if j.Username == "" || j.Celebrity == "" || c.playerID == "" {
				continue
			}

			h.mu.Lock()

			// Check for collisions (excluding this player's own entry if it exists)
			collisionField := ""
			for _, p := range h.players {
				if p.PlayerID == c.playerID {
					continue
				}
				if p.Username == j.Username {
					collisionField = "username"
					break
				}
				if p.Celebrity == j.Celebrity {
					collisionField = "celebrity"
					break
				}
			}

			if collisionField != "" {
				msgText := "That value is already taken."
				switch collisionField {
				case "username":
					msgText = "That username is already taken. Please choose a different username."
				case "celebrity":
					msgText = "That celebrity name has already been used. Please choose a different celebrity."
				}

				select {
				case c.send <- CollisionMessage{
					Type:    "collision",
					Field:   collisionField,
					Message: msgText,
				}:
				default:
					delete(h.clients, c)
					close(c.send)
				}

				h.mu.Unlock()
				continue
			}

			// Upsert player based on playerID (cookie)
			idx := -1
			for i, p := range h.players {
				if p.PlayerID == c.playerID {
					idx = i
					break
				}
			}
			if idx >= 0 {
				h.players[idx].Username = j.Username
				h.players[idx].Celebrity = j.Celebrity
			} else {
				h.players = append(h.players, Player{
					PlayerID:  c.playerID,
					Username:  j.Username,
					Celebrity: j.Celebrity,
				})
			}

			celebs := h.currentCelebritiesLocked()
			for client := range h.clients {
				select {
				case client.send <- CelebrityListMessage{
					Type:        "celebrity_list",
					Celebrities: celebs,
				}:
				default:
					delete(h.clients, client)
					close(client.send)
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) currentCelebritiesLocked() []string {
	celebs := make([]string, 0, len(h.players))
	for _, p := range h.players {
		celebs = append(celebs, p.Celebrity)
	}
	return celebs
}

// scheduleRemoval waits for d, and if no client with this playerID
// is currently connected, removes that player's entry and broadcasts
// the updated list.
func (h *Hub) scheduleRemoval(playerID string, d time.Duration) {
	time.Sleep(d)

	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients {
		if client.playerID == playerID {
			return
		}
	}

	dst := h.players[:0]
	changed := false
	for _, p := range h.players {
		if p.PlayerID == playerID {
			changed = true
			continue
		}
		dst = append(dst, p)
	}
	h.players = dst

	if !changed {
		return
	}

	celebs := h.currentCelebritiesLocked()
	for client := range h.clients {
		select {
		case client.send <- CelebrityListMessage{
			Type:        "celebrity_list",
			Celebrities: celebs,
		}:
		default:
			delete(h.clients, client)
			close(client.send)
		}
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

const playerCookieName = "partybox_id"

func getOrSetPlayerID(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(playerCookieName); err == nil && c.Value != "" {
		return c.Value
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		log.Println("rand.Read error:", err)
		return ""
	}
	id := hex.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     playerCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return id
}

// GameManager holds a set of hubs keyed by game ID, so each $path/$gameid
// is its own isolated session.
type GameManager struct {
	mu   sync.Mutex
	hubs map[string]*Hub
}

func newGameManager() *GameManager {
	return &GameManager{
		hubs: make(map[string]*Hub),
	}
}

func (gm *GameManager) getHub(gameID string) *Hub {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if hub, ok := gm.hubs[gameID]; ok {
		return hub
	}

	hub := newHub()
	gm.hubs[gameID] = hub
	go hub.run()
	return hub
}

// WebSocket handler that picks the hub based on :gameid
func serveWSForManager(gm *GameManager) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		gameID := ps.ByName("gameid")
		if gameID == "" {
			http.Error(w, "missing game id", http.StatusBadRequest)
			return
		}

		playerID := getOrSetPlayerID(w, r)
		if playerID == "" {
			http.Error(w, "unable to assign player id", http.StatusInternalServerError)
			return
		}

		hub := gm.getHub(gameID)

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("upgrade error:", err)
			return
		}

		client := &Client{
			conn:     conn,
			send:     make(chan interface{}, 8),
			playerID: playerID,
		}

		hub.register <- client

		go client.writePump()
		client.readPump(hub)
	}
}

func (c *Client) readPump(h *Hub) {
	defer func() {
		h.unregister <- c
		c.conn.Close()
	}()

	for {
		var msg JoinMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}

		if msg.Type == "join" {
			h.joins <- joinRequest{
				client: c,
				msg:    msg,
			}
		}
	}
}

func (c *Client) writePump() {
	defer c.conn.Close()

	for msg := range c.send {
		if err := c.conn.WriteJSON(msg); err != nil {
			return
		}
	}
}

// Simple HTML client for quick testing
const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Partybox Celebrity Game</title>
<style>
  body { font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem; }
  h1 { margin-bottom: 0.5rem; }
  #status { margin-bottom: 1rem; font-size: 0.9rem; }
  #celebs { margin-top: 1rem; padding: 0; list-style: none; }
  #celebs li { padding: 0.25rem 0; border-bottom: 1px solid #ddd; }
</style>
</head>
<body>
<h1>Celebrity Game</h1>
<div id="status">Connecting…</div>
<ul id="celebs"></ul>

<script>
(function() {
  const statusEl = document.getElementById('status');
  const celebsEl = document.getElementById('celebs');

  let username = '';
  let celeb = '';

  const proto = (location.protocol === 'https:') ? 'wss://' : 'ws://';
  const wsPath = location.pathname.replace(/\/$/, '') + '/ws';
  const ws = new WebSocket(proto + location.host + wsPath);

  ws.onopen = function() {
    statusEl.textContent = 'Connected.';

    username = prompt('Enter your username:') || '';
    celeb = prompt('Enter a celebrity name:') || '';
    if (username && celeb) {
      ws.send(JSON.stringify({
        type: 'join',
        username: username,
        celebrity: celeb
      }));
    }
  };

  ws.onmessage = function(event) {
    try {
      const msg = JSON.parse(event.data);

      if (msg.type === 'celebrity_list' && Array.isArray(msg.celebrities)) {
        celebsEl.innerHTML = '';
        msg.celebrities.forEach(function(c) {
          const li = document.createElement('li');
          li.textContent = c;
          celebsEl.appendChild(li);
        });
        return;
      }

      if (msg.type === 'collision') {
        statusEl.textContent = msg.message;

        if (msg.field === 'username') {
          const newUsername = prompt(msg.message, username) || '';
          if (!newUsername) {
            return;
          }
          username = newUsername;
        } else if (msg.field === 'celebrity') {
          const newCeleb = prompt(msg.message, celeb) || '';
          if (!newCeleb) {
            return;
          }
          celeb = newCeleb;
        }

        if (username && celeb) {
          ws.send(JSON.stringify({
            type: 'join',
            username: username,
            celebrity: celeb
          }));
        }
        return;
      }
    } catch (e) {
      console.error('bad message', e);
    }
  };

  ws.onclose = function() {
    statusEl.textContent = 'Disconnected.';
  };

  ws.onerror = function() {
    statusEl.textContent = 'Error with WebSocket.';
  };
})();
</script>
</body>
</html>
`

func indexHandler(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

func redirectNewGame(path string) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		gameID := randomGameID(8)
		http.Redirect(w, r, path+"/"+gameID, http.StatusTemporaryRedirect)
	}
}

func registerGame(path string, mux *httprouter.Router) {
	gm := newGameManager()

	// Root path → redirect to new random game
	mux.GET(path, redirectNewGame(path))

	// Per-game client view
	mux.GET(path+"/:gameid", indexHandler)

	// Per-game websocket
	mux.GET(path+"/:gameid/ws", serveWSForManager(gm))
}
