// Partybox Celebrity Game
//
// Each player provides their username and the name of a celebrity or famous figure,
// alive or dead, fictional or real. These are combined into a list and provided
// to a moderator. The list of celebrities (but not who provided each name) is
// shown to all players, but only the moderator can see the mapping.
//
// Features:
// - WebSockets per game ID: /path/:gameid and /path/:gameid/ws
// - First connection to a game becomes moderator (no username/celebrity)
// - Moderator can see username ↔ celebrity mapping
// - Moderator can lock/unlock lobby (no new players when locked)
// - Moderator can kick players
// - Players identified by cookie (playerID)
// - Duplicate usernames and celebrity names prevented across players
// - Collision messages sent only to the offending client
// - Games auto-reaped after configurable idle timeout
// - Random 8-char game IDs via crypto/rand, with server-side collision check
// - Turn-based guessing after moderator presses "Start Game"
// - Correctly guessed celebrities are removed from the list
// - Game ends when only one player remains in
// - Teams are tracked as guessed players join the guesser's team
// - In-browser QR button to share the current session, backed by go-qrcode

package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/julienschmidt/httprouter"
	"github.com/skip2/go-qrcode"
)

// Player holds the data we store server-side
type Player struct {
	PlayerID  string
	Username  string
	Celebrity string
}

// Messages coming from clients
type ClientMessage struct {
	Type           string `json:"type"`                      // "join", "lock_lobby", "kick", "start_game", "guess"
	Username       string `json:"username,omitempty"`        // join
	Celebrity      string `json:"celebrity,omitempty"`       // join / guess
	Lock           *bool  `json:"lock,omitempty"`            // lock_lobby
	TargetUsername string `json:"target_username,omitempty"` // kick / guess
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

// SimpleMessage is for generic notifications ("kicked", "lobby_locked", etc.)
type SimpleMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// LobbyStateMessage informs clients about lock/unlock changes.
type LobbyStateMessage struct {
	Type   string `json:"type"` // "lobby_state"
	Locked bool   `json:"locked"`
}

// SessionInfoMessage is sent immediately on connect so the client knows
// whether the lobby is locked and what role this cookie has.
type SessionInfoMessage struct {
	Type        string `json:"type"`               // "session_info"
	LobbyLocked bool   `json:"lobby_locked"`       // current lobby lock state
	IsExisting  bool   `json:"is_existing"`        // true if this cookie already has a player
	IsModerator bool   `json:"is_moderator"`       // true if this cookie is the moderator
	Username    string `json:"username,omitempty"` // known username for this cookie, if any
}

// ModeratorViewMessage is sent only to the moderator with full mapping.
type ModeratorViewMessage struct {
	Type        string            `json:"type"` // "moderator_view"
	Players     []ModeratorPlayer `json:"players"`
	LobbyLocked bool              `json:"lobby_locked"`
	CreatedAt   time.Time         `json:"created_at"`
	LastActive  time.Time         `json:"last_active"`
}

type ModeratorPlayer struct {
	Username  string `json:"username"`
	Celebrity string `json:"celebrity"`
}

// TeamState is sent as part of game_state to show teams.
type TeamState struct {
	Leader  string   `json:"leader"`
	Members []string `json:"members"`
}

// GameStateMessage broadcasts whose turn it is, who is out, and teams.
type GameStateMessage struct {
	Type        string      `json:"type"`                   // "game_state"
	Started     bool        `json:"started"`                // game started or not
	CurrentTurn string      `json:"current_turn,omitempty"` // username whose turn it is
	TurnOrder   []string    `json:"turn_order,omitempty"`   // ordered usernames
	Eliminated  []string    `json:"eliminated,omitempty"`   // usernames that are out
	Winner      string      `json:"winner,omitempty"`       // winner username when game over
	Teams       []TeamState `json:"teams,omitempty"`        // current teams
}

// GuessResultMessage informs everyone about a guess outcome.
type GuessResultMessage struct {
	Type      string `json:"type"`              // "guess_result"
	Correct   bool   `json:"correct"`           // true if guess was correct
	Guesser   string `json:"guesser"`           // username of guesser
	Target    string `json:"target"`            // username guessed
	Celebrity string `json:"celebrity"`         // celebrity guessed
	Message   string `json:"message,omitempty"` // human-readable summary
}

type Client struct {
	conn     *websocket.Conn
	send     chan any
	playerID string
}

type joinRequest struct {
	client *Client
	msg    ClientMessage
}

type modCommand struct {
	client *Client
	msg    ClientMessage
}

type guessRequest struct {
	client *Client
	msg    ClientMessage
}

type Hub struct {
	id      string
	clients map[*Client]bool
	players []Player

	register chan *Client
	unreg    chan *Client
	joins    chan joinRequest
	mods     chan modCommand
	guesses  chan guessRequest

	mu sync.RWMutex

	createdAt         time.Time
	lastActive        time.Time
	lobbyLocked       bool
	moderatorPlayerID string // cookie/playerID of moderator (never in players)

	gameStarted bool
	turnOrder   []string          // slice of PlayerID in turn order
	currentTurn int               // index into turnOrder
	eliminated  map[string]bool   // PlayerID -> out?
	teams       map[string]string // union-find parent: playerID -> parentID
}

func newHub(gameID string) *Hub {
	now := time.Now()
	return &Hub{
		id:         gameID,
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unreg:      make(chan *Client),
		joins:      make(chan joinRequest),
		mods:       make(chan modCommand),
		guesses:    make(chan guessRequest),
		createdAt:  now,
		lastActive: now,
		eliminated: make(map[string]bool),
		teams:      make(map[string]string),
	}
}

func (h *Hub) run(cfg *Config) {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.lastActive = time.Now()

			// First connection becomes moderator
			if h.moderatorPlayerID == "" {
				h.moderatorPlayerID = c.playerID
			}

			// Is this cookie already associated with a player?
			isExisting := false
			existingName := ""
			for _, p := range h.players {
				if p.PlayerID == c.playerID {
					isExisting = true
					existingName = p.Username
					break
				}
			}
			isModerator := (h.moderatorPlayerID == c.playerID)

			h.clients[c] = true

			// Send session_info first, so client decides whether/how to prompt.
			c.send <- SessionInfoMessage{
				Type:        "session_info",
				LobbyLocked: h.lobbyLocked,
				IsExisting:  isExisting,
				IsModerator: isModerator,
				Username:    existingName,
			}

			// Decide what celeb list this client is allowed to see:
			var celebs []string
			if h.gameStarted || isModerator {
				celebs = h.currentCelebritiesLocked()
			} else {
				celebs = []string{}
			}

			h.mu.Unlock()

			// Then send celeb list (possibly empty) to this client only
			c.send <- CelebrityListMessage{
				Type:        "celebrity_list",
				Celebrities: celebs,
			}

		case c := <-h.unreg:
			h.mu.Lock()
			h.lastActive = time.Now()

			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			playerID := c.playerID
			isModerator := (playerID == h.moderatorPlayerID)
			h.mu.Unlock()

			// Moderator "leaving" does not erase players.
			if playerID != "" && !isModerator {
				go h.scheduleRemoval(playerID, cfg.playerTimeout)
			}

		case jr := <-h.joins:
			h.handleJoin(cfg, jr)

		case cmd := <-h.mods:
			h.handleModCommand(cmd)

		case gr := <-h.guesses:
			h.handleGuess(cfg, gr)
		}
	}
}

// Only returns celebrities that are still "in" during the game.
// Before the game starts, all entered celebrities are shown.
func (h *Hub) currentCelebritiesLocked() []string {
	celebs := make([]string, 0, len(h.players))
	for _, p := range h.players {
		if h.gameStarted && h.eliminated[p.PlayerID] {
			continue
		}
		celebs = append(celebs, p.Celebrity)
	}
	return celebs
}

func (h *Hub) idToUsernameLocked() map[string]string {
	m := make(map[string]string, len(h.players))
	for _, p := range h.players {
		m[p.PlayerID] = p.Username
	}
	return m
}

// broadcastCelebritiesLocked broadcasts the celebrity list, but only shows
// it to the moderator before the game starts; others see an empty list.
// After the game has started, everyone sees the full (pruned) list.
func (h *Hub) broadcastCelebritiesLocked() {
	celebsAll := h.currentCelebritiesLocked()

	for client := range h.clients {
		var celebs []string
		if h.gameStarted || client.playerID == h.moderatorPlayerID {
			celebs = celebsAll
		} else {
			celebs = []string{}
		}

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

// Union-find helpers for teams
func (h *Hub) teamFindLocked(id string) string {
	parent, ok := h.teams[id]
	if !ok {
		h.teams[id] = id
		return id
	}
	if parent == id {
		return id
	}
	root := h.teamFindLocked(parent)
	h.teams[id] = root
	return root
}

func (h *Hub) teamUnionLocked(a, b string) {
	ra := h.teamFindLocked(a)
	rb := h.teamFindLocked(b)
	if ra == rb {
		return
	}
	h.teams[rb] = ra
}

// broadcastGameStateLocked sends the current game state to all clients.
func (h *Hub) broadcastGameStateLocked() {
	idToUser := h.idToUsernameLocked()

	turnNames := make([]string, 0, len(h.turnOrder))
	for _, pid := range h.turnOrder {
		if name, ok := idToUser[pid]; ok {
			turnNames = append(turnNames, name)
		}
	}

	elimNames := make([]string, 0, len(h.eliminated))
	for pid, out := range h.eliminated {
		if out {
			if name, ok := idToUser[pid]; ok {
				elimNames = append(elimNames, name)
			}
		}
	}

	var currentName string
	if h.gameStarted && len(h.turnOrder) > 0 && h.currentTurn >= 0 && h.currentTurn < len(h.turnOrder) {
		if name, ok := idToUser[h.turnOrder[h.currentTurn]]; ok {
			currentName = name
		}
	}

	// Winner if game is not started and exactly one active player remains.
	winnerName := ""
	if !h.gameStarted {
		activeCount := 0
		var lastActiveID string
		for _, p := range h.players {
			if h.eliminated[p.PlayerID] {
				continue
			}
			activeCount++
			lastActiveID = p.PlayerID
		}
		if activeCount == 1 {
			if name, ok := idToUser[lastActiveID]; ok {
				winnerName = name
			}
		}
	}

	// Build team listing from union-find structure.
	teamBuckets := make(map[string][]string)
	for _, p := range h.players {
		root := h.teamFindLocked(p.PlayerID)
		teamBuckets[root] = append(teamBuckets[root], p.Username)
	}

	teams := make([]TeamState, 0, len(teamBuckets))
	for root, members := range teamBuckets {
		leaderName := idToUser[root]
		if leaderName == "" {
			leaderName = "(unknown)"
		}
		ts := TeamState{
			Leader: leaderName,
		}
		for _, name := range members {
			if name == leaderName {
				continue
			}
			ts.Members = append(ts.Members, name)
		}
		teams = append(teams, ts)
	}

	msg := GameStateMessage{
		Type:        "game_state",
		Started:     h.gameStarted,
		CurrentTurn: currentName,
		TurnOrder:   turnNames,
		Eliminated:  elimNames,
		Winner:      winnerName,
		Teams:       teams,
	}

	for client := range h.clients {
		select {
		case client.send <- msg:
		default:
			delete(h.clients, client)
			close(client.send)
		}
	}
}

// startGameLocked freezes and shuffles the turn order and marks the game started.
func (h *Hub) startGameLocked() {
	if h.gameStarted {
		return
	}
	if len(h.players) == 0 {
		return
	}

	ids := make([]string, 0, len(h.players))
	for _, p := range h.players {
		ids = append(ids, p.PlayerID)
	}

	// Fisher-Yates shuffle using crypto/rand
	for i := len(ids) - 1; i > 0; i-- {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			continue
		}
		j := int(b[0]) % (i + 1)
		ids[i], ids[j] = ids[j], ids[i]
	}

	h.turnOrder = ids
	h.currentTurn = 0
	h.gameStarted = true
	if h.eliminated == nil {
		h.eliminated = make(map[string]bool)
	}
	if h.teams == nil {
		h.teams = make(map[string]string)
	}

	// Once the game starts, everyone is allowed to see the celebrity list.
	h.broadcastCelebritiesLocked()
	h.broadcastGameStateLocked()
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
			delete(h.eliminated, p.PlayerID)
			delete(h.teams, p.PlayerID)
			continue
		}
		dst = append(dst, p)
	}
	h.players = dst

	if !changed {
		return
	}

	h.lastActive = time.Now()

	h.broadcastCelebritiesLocked()
	h.sendModeratorViewLocked()
}

// handleJoin processes "join" messages.
func (h *Hub) handleJoin(cfg *Config, jr joinRequest) {
	msg := jr.msg
	c := jr.client

	if msg.Username == "" || msg.Celebrity == "" || c.playerID == "" {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.lastActive = time.Now()

	existingIndex := -1
	for i, p := range h.players {
		if p.PlayerID == c.playerID {
			existingIndex = i
			break
		}
	}

	if h.lobbyLocked && existingIndex == -1 {
		select {
		case c.send <- SimpleMessage{
			Type:    "lobby_locked",
			Message: "The lobby is locked; no new players may join.",
		}:
		default:
			delete(h.clients, c)
			close(c.send)
		}
		return
	}

	collisionField := ""
	for _, p := range h.players {
		if p.PlayerID == c.playerID {
			continue
		}
		if p.Username == msg.Username {
			collisionField = "username"
			break
		}
		if p.Celebrity == msg.Celebrity {
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
		return
	}

	if existingIndex >= 0 {
		h.players[existingIndex].Username = msg.Username
		h.players[existingIndex].Celebrity = msg.Celebrity
	} else {
		h.players = append(h.players, Player{
			PlayerID:  c.playerID,
			Username:  msg.Username,
			Celebrity: msg.Celebrity,
		})
		logf(cfg, "GAMES: Player %s joined %s", msg.Username, h.id)
	}

	h.broadcastCelebritiesLocked()
	h.sendModeratorViewLocked()
}

// handleGuess processes a player's guess during the game.
func (h *Hub) handleGuess(cfg *Config, gr guessRequest) {
	c := gr.client
	msg := gr.msg

	if c.playerID == "" || msg.Celebrity == "" || msg.TargetUsername == "" {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.lastActive = time.Now()

	if !h.gameStarted || len(h.turnOrder) == 0 {
		return
	}

	var guesser *Player
	for i := range h.players {
		if h.players[i].PlayerID == c.playerID {
			guesser = &h.players[i]
			break
		}
	}
	if guesser == nil {
		return
	}

	if h.eliminated[guesser.PlayerID] {
		return
	}

	if h.turnOrder[h.currentTurn] != guesser.PlayerID {
		select {
		case c.send <- SimpleMessage{
			Type:    "not_your_turn",
			Message: "It is not your turn to guess.",
		}:
		default:
		}
		return
	}

	var owner *Player
	for i := range h.players {
		if h.players[i].Celebrity == msg.Celebrity {
			owner = &h.players[i]
			break
		}
	}
	if owner == nil {
		select {
		case c.send <- SimpleMessage{
			Type:    "guess_error",
			Message: "That celebrity is not in the list.",
		}:
		default:
		}
		return
	}

	correct := (owner.Username == msg.TargetUsername)

	var text string
	if correct {
		h.eliminated[owner.PlayerID] = true
		h.teamUnionLocked(guesser.PlayerID, owner.PlayerID)
		text = guesser.Username + " correctly guessed that \"" + owner.Celebrity + "\" belongs to " + owner.Username + "."
		logf(cfg, "GAMES: %s correctly guessed %s for %s in %s", guesser.Username, owner.Username, owner.Celebrity, h.id)

		// Check if game should end (only one active player left).
		activeCount := 0
		for _, p := range h.players {
			if h.eliminated[p.PlayerID] {
				continue
			}
			activeCount++
		}
		if activeCount <= 1 {
			h.gameStarted = false
		}
	} else {
		text = guesser.Username + " incorrectly guessed that \"" + msg.Celebrity + "\" belongs to " + msg.TargetUsername + "."
		logf(cfg, "GAMES: %s incorrectly guessed %s for %s in %s", guesser.Username, msg.TargetUsername, msg.Celebrity, h.id)

		if len(h.turnOrder) > 1 {
			for i := 1; i <= len(h.turnOrder); i++ {
				next := (h.currentTurn + i) % len(h.turnOrder)
				if !h.eliminated[h.turnOrder[next]] {
					h.currentTurn = next
					break
				}
			}
		}
	}

	result := GuessResultMessage{
		Type:      "guess_result",
		Correct:   correct,
		Guesser:   guesser.Username,
		Target:    msg.TargetUsername,
		Celebrity: msg.Celebrity,
		Message:   text,
	}

	for client := range h.clients {
		select {
		case client.send <- result:
		default:
			delete(h.clients, client)
			close(client.send)
		}
	}

	// Update celebrity list (with visibility rules) and game state.
	h.broadcastCelebritiesLocked()
	h.broadcastGameStateLocked()
}

// handleModCommand processes moderator commands: lock/unlock lobby, kick users,
// start the game.
func (h *Hub) handleModCommand(cmd modCommand) {
	c := cmd.client
	msg := cmd.msg

	h.mu.Lock()
	defer h.mu.Unlock()

	h.lastActive = time.Now()

	// Only moderator may issue these commands
	if h.moderatorPlayerID == "" || c.playerID != h.moderatorPlayerID {
		return
	}

	switch msg.Type {
	case "lock_lobby":
		locked := msg.Lock != nil && *msg.Lock
		h.lobbyLocked = locked

		// Broadcast lobby state
		for client := range h.clients {
			select {
			case client.send <- LobbyStateMessage{
				Type:   "lobby_state",
				Locked: locked,
			}:
			default:
				delete(h.clients, client)
				close(client.send)
			}
		}
		h.sendModeratorViewLocked()

	case "kick":
		target := msg.TargetUsername
		if target == "" {
			return
		}

		dst := h.players[:0]
		changed := false
		kickedPlayerID := ""

		for _, p := range h.players {
			if p.Username == target {
				changed = true
				kickedPlayerID = p.PlayerID
				delete(h.eliminated, p.PlayerID)
				delete(h.teams, p.PlayerID)
				continue
			}
			dst = append(dst, p)
		}
		h.players = dst

		if !changed || kickedPlayerID == "" {
			return
		}

		for client := range h.clients {
			if client.playerID == kickedPlayerID {
				client.send <- SimpleMessage{
					Type:    "kicked",
					Message: "You have been removed by the moderator.",
				}
				delete(h.clients, client)
				close(client.send)
			}
		}

		h.broadcastCelebritiesLocked()
		h.sendModeratorViewLocked()

	case "start_game":
		h.startGameLocked()
	}
}

// sendModeratorViewLocked assumes h.mu is already held.
func (h *Hub) sendModeratorViewLocked() {
	if h.moderatorPlayerID == "" {
		return
	}

	var modClient *Client
	for c := range h.clients {
		if c.playerID == h.moderatorPlayerID {
			modClient = c
			break
		}
	}
	if modClient == nil {
		return
	}

	players := make([]ModeratorPlayer, 0, len(h.players))
	for _, p := range h.players {
		players = append(players, ModeratorPlayer{
			Username:  p.Username,
			Celebrity: p.Celebrity,
		})
	}

	msg := ModeratorViewMessage{
		Type:        "moderator_view",
		Players:     players,
		LobbyLocked: h.lobbyLocked,
		CreatedAt:   h.createdAt,
		LastActive:  h.lastActive,
	}

	select {
	case modClient.send <- msg:
	default:
		delete(h.clients, modClient)
		close(modClient.send)
	}
}

// closeAll disconnects all clients of this hub (used by reaper).
func (h *Hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.clients {
		close(c.send)
		_ = c.conn.Close()
		delete(h.clients, c)
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
	mu          sync.Mutex
	hubs        map[string]*Hub
	idleTimeout time.Duration
}

func newGameManager(idleTimeout time.Duration) *GameManager {
	gm := &GameManager{
		hubs:        make(map[string]*Hub),
		idleTimeout: idleTimeout,
	}
	if idleTimeout > 0 {
		go gm.reaperLoop()
	}
	return gm
}

func (gm *GameManager) getHub(cfg *Config, gameID string) *Hub {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if hub, ok := gm.hubs[gameID]; ok {
		return hub
	}

	hub := newHub(gameID)
	gm.hubs[gameID] = hub
	go hub.run(cfg)
	return hub
}

// newGameID generates a crypto-random game ID and ensures it doesn't
// collide with existing games.
func (gm *GameManager) newGameID() string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	for {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			panic("crypto/rand failure: " + err.Error())
		}
		out := make([]byte, 8)
		for i := range out {
			out[i] = letters[int(buf[i])%len(letters)]
		}
		id := string(out)

		gm.mu.Lock()
		_, exists := gm.hubs[id]
		gm.mu.Unlock()

		if !exists {
			return id
		}
	}
}

// reaperLoop periodically removes hubs that have been idle longer than idleTimeout.
func (gm *GameManager) reaperLoop() {
	ticker := time.NewTicker(gm.idleTimeout / 2)
	for range ticker.C {
		cutoff := time.Now().Add(-gm.idleTimeout)

		gm.mu.Lock()
		for id, hub := range gm.hubs {
			hub.mu.RLock()
			last := hub.lastActive
			hub.mu.RUnlock()

			if last.Before(cutoff) {
				delete(gm.hubs, id)
				go hub.closeAll()
			}
		}
		gm.mu.Unlock()
	}
}

// WebSocket handler that picks the hub based on :gameid
func serveWSForManager(cfg *Config, gm *GameManager) httprouter.Handle {
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

		hub := gm.getHub(cfg, gameID)

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("upgrade error:", err)
			return
		}

		client := &Client{
			conn:     conn,
			send:     make(chan any, 8),
			playerID: playerID,
		}

		hub.register <- client

		go client.writePump()
		client.readPump(hub)
	}
}

func (c *Client) readPump(h *Hub) {
	defer func() {
		h.unreg <- c
		_ = c.conn.Close()
	}()

	for {
		var msg ClientMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}

		switch msg.Type {
		case "join":
			h.joins <- joinRequest{
				client: c,
				msg:    msg,
			}
		case "lock_lobby", "kick", "start_game":
			h.mods <- modCommand{
				client: c,
				msg:    msg,
			}
		case "guess":
			h.guesses <- guessRequest{
				client: c,
				msg:    msg,
			}
		default:
			// ignore unknown types
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

// QR handler: generates a PNG QR code for the current game URL using go-qrcode.
func qrHandler(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	gameID := ps.ByName("gameid")
	if gameID == "" {
		http.Error(w, "missing game id", http.StatusBadRequest)
		return
	}

	// Derive scheme (respecting TLS and X-Forwarded-Proto if present).
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}

	// We are at /.../:gameid/qr; strip trailing "/qr" to get the game URL.
	path := r.URL.Path
	if strings.HasSuffix(path, "/qr") {
		path = strings.TrimSuffix(path, "/qr")
	}

	url := scheme + "://" + r.Host + path

	const qrSize = 320 // mobile-friendly size
	png, err := qrcode.Encode(url, qrcode.Medium, qrSize)
	if err != nil {
		http.Error(w, "qr generation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

// Simple HTML client with moderator UI, turn-based guessing + teams, and QR modal
const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Partybox - Guess the Celebrity</title>
<style>
  body { font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem; max-width: 800px; }
  #top-bar {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    margin-bottom: 0.5rem;
  }
  #top-bar h1 {
    margin: 0;
  }
  #top-bar-right {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  #user-pill {
    font-size: 0.9rem;
    padding: 0.3rem 0.7rem;
    border-radius: 999px;
    background: #f0f0f0;
    color: #333;
    white-space: nowrap;
  }
  #qr-btn {
    font-size: 0.85rem;
    padding: 0.25rem 0.6rem;
    border-radius: 999px;
    border: 1px solid #ccc;
    background: #fff;
    cursor: pointer;
  }
  #qr-btn:hover {
    background: #f5f5f5;
  }
  #status { margin-bottom: 0.25rem; font-size: 0.9rem; }
  #game-info { margin-bottom: 1rem; font-size: 0.9rem; color: #444; }
  #game-info.your-turn {
    font-weight: 600;
    color: #0b5ed7;
  }
  #celebs { margin-top: 1rem; padding: 0; list-style: none; }
  #celebs li { padding: 0.25rem 0; border-bottom: 1px solid #ddd; cursor: pointer; }
  #celebs li:hover { background: #f5f5f5; }

  #mod-panel {
    margin-top: 2rem;
    padding-top: 1rem;
    border-top: 1px solid #ccc;
    display: none;
  }
  #mod-panel h2 {
    margin-top: 0;
  }
  #players-table {
    width: 100%;
    border-collapse: collapse;
    margin-top: 0.5rem;
  }
  #players-table th, #players-table td {
    border: 1px solid #ddd;
    padding: 0.4rem 0.5rem;
    text-align: left;
  }
  #players-table th {
    background: #f5f5f5;
  }
  #lock-btn, #start-btn {
    margin-right: 0.5rem;
  }
  .kick-btn {
    padding: 0.2rem 0.5rem;
    font-size: 0.8rem;
    cursor: pointer;
  }

  #guess-modal {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.45);
    display: none;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  #guess-modal-inner {
    background: #fff;
    padding: 1rem 1.5rem;
    border-radius: 8px;
    max-width: min(90vw, 320px);
    width: 90vw;
    box-shadow: 0 4px 15px rgba(0,0,0,0.3);
    font-size: 0.9rem;
  }
  #guess-modal-inner h3 {
    margin-top: 0;
    margin-bottom: 0.5rem;
  }
  #guess-text {
    margin-bottom: 0.5rem;
  }
  #guess-target {
    width: 100%;
    margin: 0.25rem 0 0.75rem 0;
  }
  #guess-modal-buttons {
    text-align: right;
  }
  #guess-confirm, #guess-cancel {
    margin-left: 0.5rem;
  }

  #qr-modal {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.45);
    display: none;
    align-items: center;
    justify-content: center;
    z-index: 1100;
  }
  #qr-modal-inner {
    background: #fff;
    padding: 1rem 1.25rem;
    border-radius: 10px;
    max-width: min(90vw, 360px);
    width: 90vw;
    box-shadow: 0 4px 20px rgba(0,0,0,0.3);
    font-size: 0.9rem;
  }
  #qr-modal-inner h3 {
    margin-top: 0;
    margin-bottom: 0.5rem;
  }
  #qr-image-wrap {
    text-align: center;
  }
  #qr-image {
    width: 100%;
    max-width: 320px;
    height: auto;
    image-rendering: pixelated;
  }
</style>
</head>
<body>
<div id="top-bar">
  <h1>Guess the Celebrity</h1>
  <div id="top-bar-right">
    <div id="user-pill">You: <span id="user-name">(not set)</span></div>
    <button id="qr-btn" type="button" title="Show QR code for this game">Share QR Code</button>
  </div>
</div>
<div id="status">Connecting…</div>
<div id="game-info"></div>

<h2>Celebrity List</h2>
<ul id="celebs"></ul>

<div id="mod-panel">
  <h2>Moderator Controls</h2>
  <div style="margin-bottom: 0.5rem;">
    <button id="lock-btn" type="button">Lock lobby</button>
    <button id="start-btn" type="button">Start game</button>
    <span id="lock-status"></span>
  </div>

  <h3>Players</h3>
  <table id="players-table">
    <thead>
      <tr>
        <th>Username</th>
        <th>Celebrity</th>
        <th>Actions</th>
      </tr>
    </thead>
    <tbody id="players-body">
    </tbody>
  </table>
</div>

<div id="guess-modal">
  <div id="guess-modal-inner">
    <h3>Make a guess</h3>
    <div id="guess-text"></div>
    <select id="guess-target"></select>
    <div id="guess-modal-buttons">
      <button id="guess-cancel" type="button">Cancel</button>
      <button id="guess-confirm" type="button">Guess</button>
    </div>
  </div>
</div>

<div id="qr-modal">
  <div id="qr-modal-inner">
    <h3>Join this game</h3>
    <p>Scan this QR code to open the current session on another device.</p>
    <div id="qr-image-wrap">
      <img id="qr-image" alt="QR code for this session">
    </div>
    <div style="text-align:right;margin-top:0.75rem;">
      <button id="qr-close" type="button">Close</button>
    </div>
  </div>
</div>

<script>
(function() {
  const statusEl = document.getElementById('status');
  const gameInfoEl = document.getElementById('game-info');
  const celebsEl = document.getElementById('celebs');
  const userNameEl = document.getElementById('user-name');

  const modPanel = document.getElementById('mod-panel');
  const lockBtn = document.getElementById('lock-btn');
  const startBtn = document.getElementById('start-btn');
  const lockStatusEl = document.getElementById('lock-status');
  const playersBody = document.getElementById('players-body');

  const guessModal = document.getElementById('guess-modal');
  const guessTextEl = document.getElementById('guess-text');
  const guessTargetSelect = document.getElementById('guess-target');
  const guessCancelBtn = document.getElementById('guess-cancel');
  const guessConfirmBtn = document.getElementById('guess-confirm');

  const qrBtn = document.getElementById('qr-btn');
  const qrModal = document.getElementById('qr-modal');
  const qrImage = document.getElementById('qr-image');
  const qrClose = document.getElementById('qr-close');

  let username = '';
  let celeb = '';
  let isModerator = false;
  let lobbyLocked = false;
  let wasKicked = false;
  let gameStarted = false;
  let currentTurnUser = '';
  let amOut = false;
  let activePlayers = [];     // usernames of active players (not out)
  let eliminatedList = [];    // usernames of eliminated players
  let pendingCelebrity = '';

  const proto = (location.protocol === 'https:') ? 'wss://' : 'ws://';
  const wsPath = location.pathname.replace(/\/$/, '') + '/ws';
  const ws = new WebSocket(proto + location.host + wsPath);

  function sendJoin() {
    if (!username || !celeb) return;
    ws.send(JSON.stringify({
      type: 'join',
      username: username,
      celebrity: celeb
    }));
  }

  function updateLockUI() {
    lockBtn.textContent = lobbyLocked ? 'Unlock lobby' : 'Lock lobby';
    lockStatusEl.textContent = lobbyLocked
      ? 'Lobby is locked; no new players may join.'
      : 'Lobby is unlocked; new players may join.';
  }

  function renderModeratorPlayers(players) {
    playersBody.innerHTML = '';
    players.forEach(function(p) {
      const tr = document.createElement('tr');

      const tdUser = document.createElement('td');
      tdUser.textContent = p.username;

      const tdCeleb = document.createElement('td');
      tdCeleb.textContent = p.celebrity;

      const tdActions = document.createElement('td');
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'kick-btn';
      btn.dataset.username = p.username;
      btn.textContent = 'Kick';
      tdActions.appendChild(btn);

      tr.appendChild(tdUser);
      tr.appendChild(tdCeleb);
      tr.appendChild(tdActions);

      playersBody.appendChild(tr);
    });
  }

  function describeTeams(teams) {
    if (!Array.isArray(teams) || !teams.length) return '';
    const parts = teams.map(function(t) {
      const leader = t.leader || '(unknown)';
      const members = Array.isArray(t.members) && t.members.length
        ? ' [' + t.members.join(', ') + ']'
        : '';
      return leader + members;
    });
    return parts.join(' | ');
  }

  function updateGameInfo(state) {
    gameStarted = !!state.started;
    currentTurnUser = state.current_turn || '';
    eliminatedList = Array.isArray(state.eliminated) ? state.eliminated.slice() : [];
    activePlayers = [];

    if (Array.isArray(state.turn_order)) {
      state.turn_order.forEach(function(name) {
        if (eliminatedList.indexOf(name) === -1) {
          activePlayers.push(name);
        }
      });
    }

    amOut = username && eliminatedList.indexOf(username) !== -1;
    const teamsText = describeTeams(state.teams || []);

    gameInfoEl.classList.remove('your-turn');

    const lines = [];

    if (!gameStarted) {
      if (state.winner) {
        lines.push('Winner: ' + state.winner);
      }
      if (teamsText) {
        lines.push('Teams: ' + teamsText);
      }
      gameInfoEl.innerHTML = lines.map(function(t) {
        return '<div>' + t + '</div>';
      }).join('');
      if (isModerator) {
        startBtn.disabled = false;
      }
      return;
    }

    if (isModerator) {
      startBtn.disabled = true;
    }

    if (username && !amOut && currentTurnUser === username) {
      lines.push('Current turn: YOU!');
      gameInfoEl.classList.add('your-turn');
    } else if (currentTurnUser) {
      lines.push('Current turn: ' + currentTurnUser);
    } else {
      lines.push('Current turn: —');
    }

    if (eliminatedList.length) {
      lines.push('Out: ' + eliminatedList.join(', '));
    }
    if (teamsText) {
      lines.push('Teams: ' + teamsText);
    }

    gameInfoEl.innerHTML = lines.map(function(t) {
      return '<div>' + t + '</div>';
    }).join('');
  }

  function openGuessModal(celebrity) {
    if (!gameStarted) return;
    if (!username || isModerator || amOut) return;
    if (currentTurnUser && currentTurnUser !== username) {
      statusEl.textContent = 'It is ' + currentTurnUser + '\'s turn.';
      return;
    }

    const options = activePlayers.filter(function(name) {
      return name && name !== username;
    });

    if (!options.length) {
      statusEl.textContent = 'No other players to guess.';
      return;
    }

    pendingCelebrity = celebrity;
    guessTextEl.textContent = 'Whose celebrity is "' + celebrity + '"?';
    guessTargetSelect.innerHTML = '';
    options.forEach(function(name) {
      const opt = document.createElement('option');
      opt.value = name;
      opt.textContent = name;
      guessTargetSelect.appendChild(opt);
    });

    guessModal.style.display = 'flex';
  }

  function closeGuessModal() {
    pendingCelebrity = '';
    guessModal.style.display = 'none';
  }

  guessCancelBtn.addEventListener('click', function() {
    closeGuessModal();
  });

  guessConfirmBtn.addEventListener('click', function() {
    if (!pendingCelebrity) {
      closeGuessModal();
      return;
    }
    const target = guessTargetSelect.value;
    if (!target) return;
    ws.send(JSON.stringify({
      type: 'guess',
      celebrity: pendingCelebrity,
      target_username: target
    }));
    closeGuessModal();
  });

  // QR modal wiring
  qrBtn.addEventListener('click', function() {
    const base = location.pathname.replace(/\/$/, '');
    qrImage.src = base + '/qr';
    qrModal.style.display = 'flex';
  });

  qrClose.addEventListener('click', function() {
    qrModal.style.display = 'none';
  });

  qrModal.addEventListener('click', function(e) {
    if (e.target === qrModal) {
      qrModal.style.display = 'none';
    }
  });

  ws.onopen = function() {
    statusEl.textContent = 'Connected.';
  };

  ws.onmessage = function(event) {
    try {
      const msg = JSON.parse(event.data);

      if (msg.type === 'session_info') {
        lobbyLocked = !!msg.lobby_locked;
        const isExisting = !!msg.is_existing;
        isModerator = !!msg.is_moderator;
        const existingName = msg.username || '';

        if (lobbyLocked && !isExisting && !isModerator) {
          statusEl.textContent = 'Lobby is locked; no new players may join.';
          return;
        }

        if (isModerator) {
          if (existingName) {
            userNameEl.textContent = existingName;
          } else {
            userNameEl.textContent = 'Moderator';
          }
          statusEl.textContent = lobbyLocked
            ? 'You are the moderator. Lobby is locked.'
            : 'You are the moderator. Lobby is unlocked.';
          modPanel.style.display = 'block';
          updateLockUI();
          return;
        }

        if (isExisting) {
          if (existingName) {
            username = existingName;
            userNameEl.textContent = existingName;
          }
          statusEl.textContent = lobbyLocked
            ? 'Rejoined as existing player; lobby is locked.'
            : 'Rejoined as existing player.';
          return;
        }

        // New, non-moderator player, lobby is open → prompt username + celebrity
        statusEl.textContent = 'Lobby is unlocked. Please join the game.';
        username = prompt('Enter your username:') || '';
        if (!username) return;
        userNameEl.textContent = username;
        celeb = prompt('Enter a celebrity name:') || '';
        if (!celeb) return;
        sendJoin();
        return;
      }

      if (msg.type === 'celebrity_list' && Array.isArray(msg.celebrities)) {
        celebsEl.innerHTML = '';
        msg.celebrities.forEach(function(c) {
          const li = document.createElement('li');
          li.textContent = c;
          li.addEventListener('click', function() {
            openGuessModal(c);
          });
          celebsEl.appendChild(li);
        });
        return;
      }

      if (msg.type === 'collision') {
        statusEl.textContent = msg.message;

        if (msg.field === 'username') {
          const newUsername = prompt(msg.message, username) || '';
          if (!newUsername) return;
          username = newUsername;
          userNameEl.textContent = username;
        } else if (msg.field === 'celebrity') {
          const newCeleb = prompt(msg.message, celeb) || '';
          if (!newCeleb) return;
          celeb = newCeleb;
        }

        sendJoin();
        return;
      }

      if (msg.type === 'lobby_state') {
        lobbyLocked = !!msg.locked;
        if (isModerator) {
          updateLockUI();
        }
        statusEl.textContent = lobbyLocked
          ? 'Lobby is locked. No new players may join.'
          : 'Lobby is unlocked.';
        return;
      }

      if (msg.type === 'lobby_locked') {
        statusEl.textContent = msg.message;
        return;
      }

      if (msg.type === 'kicked') {
        wasKicked = true;
        const text = msg.message || 'You have been kicked.';
        statusEl.textContent = text;
        ws.close();
        return;
      }

      if (msg.type === 'moderator_view') {
        isModerator = true;
        modPanel.style.display = 'block';

        lobbyLocked = !!msg.lobby_locked;
        updateLockUI();
        if (Array.isArray(msg.players)) {
          renderModeratorPlayers(msg.players);
        }
        return;
      }

      if (msg.type === 'game_state') {
        updateGameInfo(msg);
        return;
      }

      if (msg.type === 'guess_result') {
        statusEl.textContent = msg.message || '';
        return;
      }

      if (msg.type === 'not_your_turn' || msg.type === 'guess_error') {
        statusEl.textContent = msg.message || '';
        return;
      }
    } catch (e) {
      console.error('bad message', e);
    }
  };

  ws.onclose = function() {
    if (!wasKicked) {
      statusEl.textContent = 'Disconnected.';
    }
  };

  ws.onerror = function() {
    if (!wasKicked) {
      statusEl.textContent = 'Error with WebSocket.';
    }
  };

  // Moderator: lock/unlock lobby
  lockBtn.addEventListener('click', function() {
    if (!isModerator) return;
    const newLock = !lobbyLocked;
    ws.send(JSON.stringify({
      type: 'lock_lobby',
      lock: newLock
    }));
  });

  // Moderator: start game
  startBtn.addEventListener('click', function() {
    if (!isModerator) return;
    if (gameStarted) return;
    ws.send(JSON.stringify({
      type: 'start_game'
    }));
  });

  // Moderator: kick players (event delegation on table body)
  playersBody.addEventListener('click', function(e) {
    if (!isModerator) return;
    const btn = e.target.closest('button.kick-btn');
    if (!btn) return;
    const targetUsername = btn.dataset.username;
    if (!targetUsername) return;

    if (!confirm('Kick ' + targetUsername + '?')) {
      return;
    }

    ws.send(JSON.stringify({
      type: 'kick',
      target_username: targetUsername
    }));
  });
})();
</script>
</body>
</html>
`

func indexHandler(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

// redirectNewGame handles GET /path by generating a new random game ID
// (with server-side collision detection) and redirecting to /path/:gameid.
func redirectNewGame(cfg *Config, path string, gm *GameManager) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		gameID := gm.newGameID()
		logf(cfg, "GAMES: Created game %s/%s", path, gameID)
		http.Redirect(w, r, path+"/"+gameID, http.StatusTemporaryRedirect)
	}
}

// registerGame sets up routes so that:
//   - $path             → redirects to new random game (8-char ID)
//   - $path/:gameid     → HTML client
//   - $path/:gameid/ws  → WebSocket for that game
//   - $path/:gameid/qr  → PNG QR code for that game URL
func registerGame(cfg *Config, path string, mux *httprouter.Router, idleTimeout time.Duration) {
	gm := newGameManager(idleTimeout)

	// Root path → redirect to new random game
	mux.GET(path, redirectNewGame(cfg, path, gm))

	// Per-game client view
	mux.GET(cfg.prefix+path+"/:gameid", indexHandler)

	// Per-game websocket
	mux.GET(cfg.prefix+path+"/:gameid/ws", serveWSForManager(cfg, gm))

	// Per-game QR code
	mux.GET(cfg.prefix+path+"/:gameid/qr", qrHandler)
}
