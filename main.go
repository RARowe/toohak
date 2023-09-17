package main

import (
	"encoding/json"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

func main() {
	r := mux.NewRouter().StrictSlash(true)
	admin := r.PathPrefix("/admin").Subrouter()
	admin.HandleFunc("/join/{activeGameId}", adminJoinGame).Methods(http.MethodGet)
	admin.HandleFunc("/start/{gameId}", startGame).Methods(http.MethodPost)
	admin.HandleFunc("/", listGames).Methods(http.MethodGet)
	admin.HandleFunc("/{activeGameId}", adminActiveGame).Methods(http.MethodGet)

	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		bs, _ := os.ReadFile("index.html")
		w.Write(bs)
	}).Methods(http.MethodGet)

	r.HandleFunc("/join/{activeGameId}", connectToGame).Methods(http.MethodGet)
	r.HandleFunc("/{activeGameId}", activeGame).Methods(http.MethodGet)

	http.ListenAndServe("0.0.0.0:8080", r)
}

func adminJoinGame(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	activeGameId, ok := vars["activeGameId"]
	if !ok {
		http.Error(w, "activeGameId must be specified.", http.StatusBadRequest)
		return
	}

	game, ok := activeGames[activeGameId]
	if !ok {
		http.NotFound(w, r)
		return
	}

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	_, msg, err := c.ReadMessage()
	if err != nil {
		log.Print("read:", err)
		return
	}
	strMsg := string(msg)
	log.Print(strMsg)
	c.WriteJSON(InitGameMsg{Type: INIT_GAME, Players: game.PendingPlayers})
	playerId, _ := strconv.Atoi(strMsg)
	game.AddConnForPlayer(playerId, c)
}

const PLAYERS_ADDED = "PLAYERS_ADDED"
const INIT_GAME = "INIT_GAME"

type PlayersAddedMsg struct {
	Type    string   `json:"type"`
	Players []Player `json:"players"`
}

type InitGameMsg struct {
	Type    string   `json:"type"`
	Players []Player `json:"players"`
}

func runGame(g *ActiveGame) {
	// Event loop
	for {
		select {
		case <-time.After(500 * time.Millisecond):
			addPendingPlayers(g)
		}
	}
}

func addPendingPlayers(g *ActiveGame) {
	if len(g.PendingPlayers) == 0 {
		return
	}

	playersAddedMsg := PlayersAddedMsg{
		Type:    PLAYERS_ADDED,
		Players: g.PendingPlayers,
	}
	msg, _ := json.Marshal(playersAddedMsg)
	for i := range g.Players {
		g.Players[i].Conn.WriteMessage(websocket.TextMessage, msg)
	}
	g.Players = append(g.Players, g.PendingPlayers...)
	g.PendingPlayers = nil
}

func startGame(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	gameIdStr, ok := vars["gameId"]
	if !ok {
		http.Error(w, "gameId must be specified.", http.StatusBadRequest)
		return
	}

	gameId, err := strconv.Atoi(gameIdStr)
	if err != nil {
		http.Error(w, "gameId must be an integer.", http.StatusBadRequest)
		return
	}

	var game *Game
	for _, g := range games {
		if g.Id == gameId {
			game = g
			break
		}
	}

	if game == nil {
		http.NotFound(w, r)
		return
	}

	activeGameId := RandStringRunes(3)
	activeGame := &ActiveGame{
		Id:        activeGameId,
		Name:      game.Name,
		Questions: game.Questions,
		mutex:     &sync.Mutex{},
		Chan:      make(chan string, 2048),
	}

	activeGames[activeGameId] = activeGame
	go runGame(activeGame)

	w.WriteHeader(http.StatusCreated)
	resp := map[string]string{"activeGameId": activeGameId}
	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func RandStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

var activeGames = map[string]*ActiveGame{}

type Player struct {
	Id    int             `json:"id"`
	Name  string          `json:"name"`
	Admin bool            `json:"admin"`
	Conn  *websocket.Conn `json:"-"`
}

type ActiveGame struct {
	Id             string
	Name           string
	Questions      []Question
	Current        int
	playerId       int
	mutex          *sync.Mutex
	PendingPlayers []Player
	Players        []Player
	Chan           chan string
}

func (g *ActiveGame) AddPlayer(name string) int {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.playerId += 1

	g.PendingPlayers = append(g.PendingPlayers, Player{Id: g.playerId, Name: name, Admin: name == "admin"})

	return g.playerId
}

func (g *ActiveGame) AddConnForPlayer(id int, ws *websocket.Conn) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	for i := range g.Players {
		p := &g.Players[i]
		if p.Id == id {
			p.Conn = ws
		}
	}

	// This isn't good
	for i := range g.PendingPlayers {
		p := &g.PendingPlayers[i]
		if p.Id == id {
			p.Conn = ws
		}
	}
}

type Game struct {
	Id        int
	Name      string
	Questions []Question
}

var games = []*Game{
	{
		Id:   1,
		Name: "Simple Game",
		Questions: []Question{{
			Id:            1,
			Text:          "What is the first letter of the Alphabet?",
			CorrectAnswer: 3,
			Answers: []Answer{
				{Id: 1, Text: "C"},
				{Id: 2, Text: "Z"},
				{Id: 3, Text: "A"},
				{Id: 4, Text: "X"},
			},
		}},
	},
}

func activeGame(w http.ResponseWriter, r *http.Request) {
	bs, _ := os.ReadFile("index.html")
	w.Write(bs)
}

func adminActiveGame(w http.ResponseWriter, r *http.Request) {
	type activeGameAdminPage struct {
		Name         string
		ActiveGameId string
		PlayerId     int
	}
	vars := mux.Vars(r)
	activeGameId, ok := vars["activeGameId"]
	if !ok {
		http.Error(w, "activeGameId must be specified.", http.StatusBadRequest)
		return
	}

	game, ok := activeGames[activeGameId]
	if !ok {
		http.NotFound(w, r)
		return
	}

	tmpl, err := template.ParseFiles("game-admin.tmpl.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pageData := activeGameAdminPage{
		Name:         game.Name,
		ActiveGameId: activeGameId,
		PlayerId:     game.AddPlayer("admin"),
	}

	if err := tmpl.Execute(w, pageData); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func listGames(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("list-games.tmpl.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, games); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type Answer struct {
	Id   int
	Text string
}
type Question struct {
	Id            int
	Text          string
	CorrectAnswer int
	Answers       []Answer
}

var upgrader = websocket.Upgrader{} // use default options
func connectToGame(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	activeGameId, ok := vars["activeGameId"]
	if !ok {
		http.Error(w, "activeGameId must be specified.", http.StatusBadRequest)
		return
	}

	game, ok := activeGames[activeGameId]
	if !ok {
		http.NotFound(w, r)
		return
	}

	playerId := game.AddPlayer("[NO NAME]")
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	c.WriteJSON(InitGameMsg{Type: INIT_GAME, Players: game.Players})
	game.AddConnForPlayer(playerId, c)
}
