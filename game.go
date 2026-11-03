package main

// Rules engine for "Queens". No networking in here: the server (main.go) and
// the bots (bot.go) both drive a Game through its exported methods, which
// validate every move. Callers must serialise access (the hub holds a mutex).

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
)

const (
	NumSeats  = 4  // seats 0..3 = North, East, South, West (clockwise)
	HandSize  = 13 // cards dealt to each player
	logKeep   = 60
	logToSend = 14
)

var seatNames = [NumSeats]string{"North", "East", "South", "West"}

// Seats 0 and 2 (North/South) are team 0; seats 1 and 3 (East/West) are team 1.
func teamOf(seat int) int    { return seat % 2 }
func partnerOf(seat int) int { return (seat + 2) % NumSeats }
func nextSeat(seat int) int  { return (seat + 1) % NumSeats } // clockwise from above

// ---------------------------------------------------------------- cards

type Card struct {
	ID   int    `json:"id"`   // unique physical card, 0..107
	Rank string `json:"rank"` // "2".."10","J","Q","K","A","JOKER"
	Suit string `json:"suit"` // "S","H","D","C"; jokers use "R" (red) / "B" (black)
}

func (c Card) String() string {
	if c.Rank == "JOKER" {
		return "Joker"
	}
	return c.Rank + map[string]string{"S": "♠", "H": "♥", "D": "♦", "C": "♣"}[c.Suit]
}

var baseRanks = []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"}
var suitList = []string{"S", "H", "D", "C"}

// newShoe returns two standard 52-card decks plus both jokers from each: 108 cards.
func newShoe() []Card {
	cards := make([]Card, 0, 108)
	id := 0
	for d := 0; d < 2; d++ {
		for _, s := range suitList {
			for _, r := range baseRanks {
				cards = append(cards, Card{ID: id, Rank: r, Suit: s})
				id++
			}
		}
		cards = append(cards, Card{ID: id, Rank: "JOKER", Suit: "R"})
		id++
		cards = append(cards, Card{ID: id, Rank: "JOKER", Suit: "B"})
		id++
	}
	return cards
}

// cardPoints: 3-9 = 5, 10/J/Q/K/A = 10, Q♠ = 100, 2 = 25, joker = 50.
func cardPoints(c Card) int {
	switch c.Rank {
	case "JOKER":
		return 50
	case "2":
		return 25
	case "Q":
		if c.Suit == "S" {
			return 100
		}
		return 10
	case "10", "J", "K", "A":
		return 10
	default:
		return 5
	}
}

func sumPoints(cards []Card) int {
	n := 0
	for _, c := range cards {
		n += cardPoints(c)
	}
	return n
}

// ---------------------------------------------------------------- rules

type Rules struct {
	TargetScore        int  `json:"targetScore"`
	JokersWild         bool `json:"jokersWild"`         // optional: jokers wild too, but only playable once the team is down (default off: only 2s are wild)
	PickupRequiresDown bool `json:"pickupRequiresDown"` // team must already be down to take the pile
	DealBonus          bool `json:"dealBonus"`          // reserved - see checkDealBonus
}

func DefaultRules() Rules {
	return Rules{TargetScore: 2000, JokersWild: false, PickupRequiresDown: true}
}

// ---------------------------------------------------------------- state

type Phase string

const (
	PhaseIdle     Phase = "idle"     // seats being filled, no hand dealt yet
	PhasePlaying  Phase = "playing"  // a hand is in progress
	PhaseHandOver Phase = "handover" // hand finished, waiting for the next deal
	PhaseGameOver Phase = "gameover" // a team reached the target score
)

const (
	StepDraw = "draw" // player must draw from stock or take the pile
	StepPlay = "play" // player may meld / add, then must discard
)

type Meld struct {
	ID    int    `json:"id"`
	Rank  string `json:"rank"`
	Cards []Card `json:"cards"`
	By    int    `json:"by"` // seat that started it
}

type Team struct {
	Score     int
	Melds     []*Meld
	Collector int // seat that started the team's first meld this hand; -1 if none
}

func (t *Team) Down() bool { return len(t.Melds) > 0 }

func (t *Team) meldFor(rank string) *Meld {
	for _, m := range t.Melds {
		if m.Rank == rank {
			return m
		}
	}
	return nil
}

func (t *Team) meldByID(id int) *Meld {
	for _, m := range t.Melds {
		if m.ID == id {
			return m
		}
	}
	return nil
}

type TeamResult struct {
	Team    int `json:"team"`
	MeldPts int `json:"meldPts"` // value of cards showing in melds
	HandPts int `json:"handPts"` // value of cards left in hands that was deducted
	Delta   int `json:"delta"`
	Total   int `json:"total"` // score after this hand
}

type HandResult struct {
	HandNo     int           `json:"handNo"`
	OutSeat    int           `json:"outSeat"`    // seat that went out; -1 if the stock ran dry
	Winner     int           `json:"winner"`     // team that went out; -1 if none
	FirstCount int           `json:"firstCount"` // team that counted first (the one that did not deal)
	Teams      [2]TeamResult `json:"teams"`      // indexed by team
	GameWinner int           `json:"gameWinner"` // -1 until someone reaches the target
}

type Game struct {
	Rules     Rules
	Phase     Phase
	Names     [NumSeats]string // display names, set by the hub (used in the log)
	Dealer    int
	HandNo    int
	Turn      int
	TurnStep  string
	TurnCount int // completed turns this hand
	Stock     []Card
	Discard   []Card // last element is the top
	Hands     [NumSeats][]Card
	Teams     [2]*Team
	Result    *HandResult
	Log       []string

	nextMeldID int
}

func NewGame(r Rules) *Game {
	g := &Game{Rules: r, Phase: PhaseIdle, Dealer: rand.IntN(NumSeats)}
	g.Teams[0] = &Team{Collector: -1}
	g.Teams[1] = &Team{Collector: -1}
	for i := range g.Names {
		g.Names[i] = seatNames[i]
	}
	return g
}

func (g *Game) logf(format string, a ...any) {
	g.Log = append(g.Log, fmt.Sprintf(format, a...))
	if len(g.Log) > logKeep {
		g.Log = g.Log[len(g.Log)-logKeep:]
	}
}

func (g *Game) name(seat int) string {
	if n := strings.TrimSpace(g.Names[seat]); n != "" {
		return n
	}
	return seatNames[seat]
}

func (g *Game) isJoker(c Card) bool {
	if c.Rank == "JOKER" {
		return true
	}
	return false // g.Rules.JokersWild && c.Rank == "JOKER"
}

func (g *Game) isWild(c Card) bool {
	if c.Rank == "2" {
		return true
	}
	return g.Rules.JokersWild && c.Rank == "JOKER"
}

func (g *Game) split(cards []Card) (naturals, wilds []Card) {
	for _, c := range cards {
		if g.isWild(c) {
			wilds = append(wilds, c)
		} else {
			naturals = append(naturals, c)
		}
	}
	return
}

// NextDealer is who deals the following hand.
func (g *Game) NextDealer() int { return nextSeat(g.Dealer) }

// FirstCountTeam is the team that counts its points first: the team that did not deal.
func (g *Game) FirstCountTeam() int { return 1 - teamOf(g.Dealer) }

// ---------------------------------------------------------------- dealing

// StartHand shuffles two decks plus jokers, deals 13 cards to each player and
// flips one card to start the discard pile. The player clockwise of the dealer
// goes first.
func (g *Game) StartHand() error {
	switch g.Phase {
	case PhasePlaying:
		return errors.New("a hand is already in progress")
	case PhaseGameOver:
		return errors.New("the game is over - start a new game")
	}
	if g.HandNo > 0 {
		g.Dealer = g.NextDealer()
	}
	g.HandNo++
	shoe := newShoe()
	rand.Shuffle(len(shoe), func(i, j int) { shoe[i], shoe[j] = shoe[j], shoe[i] })
	for s := 0; s < NumSeats; s++ {
		g.Hands[s] = append([]Card(nil), shoe[:HandSize]...)
		shoe = shoe[HandSize:]
	}
	g.Discard = []Card{shoe[0]}
	g.Stock = append([]Card(nil), shoe[1:]...)
	for _, t := range g.Teams {
		t.Melds = nil
		t.Collector = -1
	}
	g.Result = nil
	g.Turn = nextSeat(g.Dealer)
	g.TurnStep = StepDraw
	g.TurnCount = 0
	g.Phase = PhasePlaying
	g.logf("Hand %d: %s deals. %s is turned up. %s plays first.",
		g.HandNo, g.name(g.Dealer), g.Discard[0], g.name(g.Turn))
	g.checkDealBonus()
	return nil
}

// checkDealBonus is the hook for the instant-100-points-on-the-deal rule. The
// wording in the game notes ("If the dealer gets all 13 cards for each of the 4
// players, plus the flip card ... 1 in 20 chance") is ambiguous, so it is off
// by default (Rules.DealBonus) and intentionally does nothing until the exact
// condition is confirmed.
func (g *Game) checkDealBonus() {
	if !g.Rules.DealBonus {
		return
	}
}

// ---------------------------------------------------------------- helpers

func (g *Game) checkTurn(seat int, step string) error {
	if g.Phase != PhasePlaying {
		return errors.New("no hand is in progress")
	}
	if seat < 0 || seat >= NumSeats {
		return errors.New("you are not seated")
	}
	if g.Turn != seat {
		return errors.New("it is not your turn")
	}
	if g.TurnStep != step {
		if step == StepDraw {
			return errors.New("you have already drawn - meld, then discard")
		}
		return errors.New("draw a card (or take the pile) first")
	}
	return nil
}

// takeCards looks the ids up in the seat's hand without removing them.
func (g *Game) takeCards(seat int, ids []int) ([]Card, error) {
	index := map[int]int{}
	for i, c := range g.Hands[seat] {
		index[c.ID] = i
	}
	seen := map[int]bool{}
	cards := make([]Card, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			return nil, errors.New("the same card was selected twice")
		}
		seen[id] = true
		i, ok := index[id]
		if !ok {
			return nil, errors.New("that card is not in your hand")
		}
		cards = append(cards, g.Hands[seat][i])
	}
	return cards, nil
}

func (g *Game) removeFromHand(seat int, cards []Card) {
	gone := map[int]bool{}
	for _, c := range cards {
		gone[c.ID] = true
	}
	kept := g.Hands[seat][:0]
	for _, c := range g.Hands[seat] {
		if !gone[c.ID] {
			kept = append(kept, c)
		}
	}
	g.Hands[seat] = kept
}

// validateNewMeld checks cards form a legal new meld for the team: at least 3
// cards, at least 2 naturals of one rank, wild cards outnumbered by naturals,
// and the team does not already have a meld of that rank.
func (g *Game) validateNewMeld(team *Team, cards []Card) (string, error) {
	nat, _ := g.split(cards)

	//fmt.Printf(" \nnat[0] = %s", nat[0])

	if len(cards) < 3 && !g.isJoker(nat[0]) {
		return "", errors.New("a meld needs at least 3 cards")
	}
	// nat, wild := g.split(cards)
	if !team.Down() {
		for _, c := range cards {
			if c.Rank == "JOKER" { // } && g.Rules.JokersWild {
				return "", errors.New("jokers can only be played once your team is down")
			}
		}
	}
	if len(nat) < 2 && !g.isJoker(nat[0]) {
		return "", errors.New("a meld needs at least 2 natural cards (wild cards can't start one)")
	}
	rank := nat[0].Rank
	for _, c := range nat {
		if c.Rank != rank {
			return "", errors.New("the natural cards in a meld must all be the same rank")
		}
	}
	//if len(wild) >= len(nat) {
	//	return "", errors.New("wild cards must be outnumbered by natural cards in a meld")
	//}
	if team.meldFor(rank) != nil {
		return "", fmt.Errorf("your team already has a meld of %ss - add to it instead", rank)
	}
	return rank, nil
}

func (g *Game) validateAdd(m *Meld, cards []Card) error {
	if len(cards) == 0 {
		return errors.New("select the cards to add first")
	}
	nat, wild := g.split(m.Cards)
	n2, w2 := g.split(cards)
	for _, c := range n2 {
		if c.Rank != m.Rank {
			return fmt.Errorf("only %ss and wild cards can be added to that meld", m.Rank)
		}
	}
	if len(wild)+len(w2) >= len(nat)+len(n2) {
		return errors.New("wild cards must stay outnumbered by natural cards in a meld")
	}
	return nil
}

func (g *Game) addMeld(seat int, rank string, cards []Card) *Meld {
	g.nextMeldID++
	m := &Meld{ID: g.nextMeldID, Rank: rank, Cards: sortMeldCards(g, cards), By: seat}
	t := g.Teams[teamOf(seat)]
	if len(t.Melds) == 0 {
		t.Collector = seat
	}
	t.Melds = append(t.Melds, m)
	return m
}

// sortMeldCards puts naturals first and wilds last, for stable display.
func sortMeldCards(g *Game, cards []Card) []Card {
	out := append([]Card(nil), cards...)
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := g.isWild(out[i]), g.isWild(out[j])
		if wi != wj {
			return !wi
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ---------------------------------------------------------------- the pile

// pickupBlock returns why the seat cannot take the discard pile right now
// (ignoring what is in their hand), or "" if it is permitted.
func (g *Game) pickupBlock(seat int) string {
	switch {
	case g.Phase != PhasePlaying:
		return "no hand is in progress"
	case g.Turn != seat:
		return "it is not your turn"
	case g.TurnStep != StepDraw:
		return "you have already drawn this turn"
	case len(g.Discard) == 0:
		return "the discard pile is empty"
	case g.TurnCount == 0:
		return "the first player of a hand must draw from the stock"
	}
	t := g.Teams[teamOf(seat)]
	if g.Rules.PickupRequiresDown && !t.Down() {
		return "your team must be down (have a meld) before it can take the pile"
	}
	top := g.Discard[len(g.Discard)-1]
	if g.isWild(top) {
		return "a wild card on top blocks the pile"
	}
	if t.meldFor(top.Rank) != nil {
		return fmt.Sprintf("blocked - your team already has a meld of %ss", top.Rank)
	}
	return ""
}

// autoPickupCards picks the hand cards to pair with the top discard: two
// naturals of its rank, otherwise one natural plus one wild card. nil if the
// hand can't do either.
func (g *Game) autoPickupCards(seat int) []Card {
	top := g.Discard[len(g.Discard)-1]
	down := g.Teams[teamOf(seat)].Down()
	var nat, wild []Card
	for _, c := range g.Hands[seat] {
		switch {
		case g.isWild(c):
			if c.Rank == "JOKER" && !down {
				continue // jokers only become playable once the team is down
			}
			wild = append(wild, c)
		case c.Rank == top.Rank:
			nat = append(nat, c)
		}
	}
	switch {
	case len(nat) >= 2:
		return nat[:2]
	case len(nat) == 1 && len(wild) >= 1:
		// spend the cheapest wild card
		sort.Slice(wild, func(i, j int) bool { return cardPoints(wild[i]) < cardPoints(wild[j]) })
		return []Card{nat[0], wild[0]}
	}
	return nil
}

// CanPickup reports whether the seat may take the pile right now.
func (g *Game) CanPickup(seat int) bool {
	if g.pickupBlock(seat) != "" {
		return false
	}
	return g.autoPickupCards(seat) != nil
}

// CanDraw reports whether the seat may draw from the stock right now.
func (g *Game) CanDraw(seat int) bool {
	return g.Phase == PhasePlaying && g.Turn == seat && g.TurnStep == StepDraw && len(g.Stock) > 0
}

// ---------------------------------------------------------------- moves

// Draw takes the top card of the stock.
func (g *Game) Draw(seat int) error {
	if err := g.checkTurn(seat, StepDraw); err != nil {
		return err
	}
	if len(g.Stock) == 0 {
		return errors.New("the stock is empty - you must take the discard pile")
	}
	c := g.Stock[len(g.Stock)-1]
	g.Stock = g.Stock[:len(g.Stock)-1]
	g.Hands[seat] = append(g.Hands[seat], c)
	g.TurnStep = StepPlay
	g.logf("%s drew from the stock.", g.name(seat))
	return nil
}

// Pickup takes the whole discard pile. The top card must go straight into a new
// meld together with `ids` from the hand (two naturals of its rank, or one
// natural and a wild card). With no ids, the best pair is chosen automatically.
func (g *Game) Pickup(seat int, ids []int) error {
	if err := g.checkTurn(seat, StepDraw); err != nil {
		return err
	}
	if why := g.pickupBlock(seat); why != "" {
		return errors.New(why)
	}
	top := g.Discard[len(g.Discard)-1]
	var chosen []Card
	if len(ids) == 0 {
		chosen = g.autoPickupCards(seat)
		if chosen == nil {
			return fmt.Errorf("to take the pile you need two natural %ss, or one natural %s and a wild card, in your hand", top.Rank, top.Rank)
		}
	} else {
		var err error
		if chosen, err = g.takeCards(seat, ids); err != nil {
			return err
		}
	}
	team := g.Teams[teamOf(seat)]
	meldCards := append([]Card{top}, chosen...)
	rank, err := g.validateNewMeld(team, meldCards)
	if err != nil {
		return err
	}
	pile := append([]Card(nil), g.Discard[:len(g.Discard)-1]...)
	g.Discard = nil
	g.removeFromHand(seat, chosen)
	g.Hands[seat] = append(g.Hands[seat], pile...)
	g.addMeld(seat, rank, meldCards)
	g.TurnStep = StepPlay
	g.logf("%s took the pile (%d cards) and melded %ss.", g.name(seat), len(pile)+1, rank)
	g.afterPlay(seat) // a one-card pile can leave the hand empty: that goes out
	return nil
}

// NewMeld lays down a new meld from the hand.
func (g *Game) NewMeld(seat int, ids []int) error {
	if err := g.checkTurn(seat, StepPlay); err != nil {
		return err
	}
	cards, err := g.takeCards(seat, ids)
	if err != nil {
		return err
	}
	team := g.Teams[teamOf(seat)]
	rank, err := g.validateNewMeld(team, cards)
	if err != nil {
		return err
	}
	g.removeFromHand(seat, cards)
	g.addMeld(seat, rank, cards)
	g.logf("%s melded %d x %s.", g.name(seat), len(cards), rank)
	g.afterPlay(seat)
	return nil
}

// AddToMeld adds cards to one of the team's existing melds. Either partner may do so.
func (g *Game) AddToMeld(seat, meldID int, ids []int) error {
	if err := g.checkTurn(seat, StepPlay); err != nil {
		return err
	}
	team := g.Teams[teamOf(seat)]
	m := team.meldByID(meldID)
	if m == nil {
		return errors.New("that is not one of your team's melds")
	}
	cards, err := g.takeCards(seat, ids)
	if err != nil {
		return err
	}
	if err := g.validateAdd(m, cards); err != nil {
		return err
	}
	g.removeFromHand(seat, cards)
	m.Cards = sortMeldCards(g, append(append([]Card(nil), m.Cards...), cards...))
	g.logf("%s added %d card(s) to the %ss.", g.name(seat), len(cards), m.Rank)
	g.afterPlay(seat)
	return nil
}

// afterPlay: a player who melds their last card goes out without discarding.
func (g *Game) afterPlay(seat int) {
	if len(g.Hands[seat]) == 0 {
		g.endHand(seat)
	}
}

// DiscardCard ends the turn. Discarding the last card in hand goes out.
func (g *Game) DiscardCard(seat, cardID int) error {
	if err := g.checkTurn(seat, StepPlay); err != nil {
		return err
	}
	cards, err := g.takeCards(seat, []int{cardID})
	if err != nil {
		return err
	}
	g.removeFromHand(seat, cards)
	g.Discard = append(g.Discard, cards[0])
	g.logf("%s discarded %s.", g.name(seat), cards[0])
	if len(g.Hands[seat]) == 0 {
		g.endHand(seat)
		return nil
	}
	g.advanceTurn()
	return nil
}

func (g *Game) advanceTurn() {
	g.TurnCount++
	g.Turn = nextSeat(g.Turn)
	g.TurnStep = StepDraw
	// With the stock gone the hand ends unless the next player can take the pile.
	if len(g.Stock) == 0 && !g.CanPickup(g.Turn) {
		g.logf("The stock is empty - the hand ends.")
		g.endHand(-1)
	}
}

// SetHandOrder lets a player keep their cards in their own order (the touch
// "move cards around" feature). ids must be exactly the cards in the hand; it
// is allowed at any time, even when it is not the player's turn.
func (g *Game) SetHandOrder(seat int, ids []int) error {
	if seat < 0 || seat >= NumSeats {
		return errors.New("you are not seated")
	}
	hand := g.Hands[seat]
	if len(ids) != len(hand) {
		return errors.New("that is not your whole hand")
	}
	byID := make(map[int]Card, len(hand))
	for _, c := range hand {
		byID[c.ID] = c
	}
	out := make([]Card, 0, len(hand))
	for _, id := range ids {
		c, ok := byID[id]
		if !ok {
			return errors.New("that card is not in your hand")
		}
		delete(byID, id) // a repeated id will fail the lookup
		out = append(out, c)
	}
	g.Hands[seat] = out
	return nil
}

// ---------------------------------------------------------------- scoring

func (g *Game) meldPoints(team int) int {
	n := 0
	for _, m := range g.Teams[team].Melds {
		n += sumPoints(m.Cards)
	}
	return n
}

func (g *Game) handPoints(team int) int {
	n := 0
	for s := 0; s < NumSeats; s++ {
		if teamOf(s) == team {
			n += sumPoints(g.Hands[s])
		}
	}
	return n
}

// endHand scores the hand. outSeat is the player who went out, or -1 when the
// stock ran dry (then both teams are scored as losers).
//
// The team that did not deal counts first. The team that went out adds the
// points showing in its melds; the other team first deducts the cards left in
// both partners' hands, then adds its melds. The first team to reach the target
// wins - and because the non-dealing team counts first, it wins a tie.
func (g *Game) endHand(outSeat int) {
	winner := -1
	if outSeat >= 0 {
		winner = teamOf(outSeat)
	}
	first := g.FirstCountTeam()
	res := &HandResult{HandNo: g.HandNo, OutSeat: outSeat, Winner: winner, FirstCount: first, GameWinner: -1}
	for _, t := range []int{first, 1 - first} {
		tr := TeamResult{Team: t, MeldPts: g.meldPoints(t)}
		if t == winner {
			tr.Delta = tr.MeldPts
		} else {
			tr.HandPts = g.handPoints(t)
			tr.Delta = tr.MeldPts - tr.HandPts
		}
		g.Teams[t].Score += tr.Delta
		tr.Total = g.Teams[t].Score
		res.Teams[t] = tr
		if res.GameWinner < 0 && tr.Total >= g.Rules.TargetScore {
			res.GameWinner = t
		}
	}
	g.Result = res
	if outSeat >= 0 {
		g.logf("%s went out!", g.name(outSeat))
	}
	if res.GameWinner >= 0 {
		g.Phase = PhaseGameOver
	} else {
		g.Phase = PhaseHandOver
	}
}

// ---------------------------------------------------------------- views

type MeldView struct {
	ID     int    `json:"id"`
	Rank   string `json:"rank"`
	Cards  []Card `json:"cards"`
	By     int    `json:"by"`
	Points int    `json:"points"`
}

type TeamView struct {
	Score      int        `json:"score"`
	Down       bool       `json:"down"`
	Collector  int        `json:"collector"`
	Melds      []MeldView `json:"melds"`
	MeldPoints int        `json:"meldPoints"`
}

// View is everything a client may see. Other players' cards are never included,
// only how many they hold.
type View struct {
	Phase         Phase       `json:"phase"`
	Rules         Rules       `json:"rules"`
	HandNo        int         `json:"handNo"`
	Dealer        int         `json:"dealer"`
	NextDealer    int         `json:"nextDealer"`
	FirstCount    int         `json:"firstCount"`
	Turn          int         `json:"turn"`
	TurnStep      string      `json:"turnStep"`
	StockCount    int         `json:"stockCount"`
	DiscardCount  int         `json:"discardCount"`
	DiscardTop    *Card       `json:"discardTop"`
	DiscardRecent []Card      `json:"discardRecent"`
	HandCounts    [4]int      `json:"handCounts"`
	Teams         [2]TeamView `json:"teams"`
	Result        *HandResult `json:"result"`
	Log           []string    `json:"log"`
	Hand          []Card      `json:"hand,omitempty"` // only the viewer's own hand
	CanDraw       bool        `json:"canDraw"`
	CanPickup     bool        `json:"canPickup"`
	PickupBlocked string      `json:"pickupBlocked,omitempty"`
}

// View builds the view for a seat (use -1 for a spectator).
func (g *Game) View(seat int) View {
	v := View{
		Phase: g.Phase, Rules: g.Rules, HandNo: g.HandNo,
		Dealer: g.Dealer, NextDealer: g.NextDealer(), FirstCount: g.FirstCountTeam(),
		Turn: g.Turn, TurnStep: g.TurnStep,
		StockCount: len(g.Stock), DiscardCount: len(g.Discard),
		Result: g.Result, DiscardRecent: []Card{}, // never null: the pile can be empty after a pickup
	}
	if n := len(g.Discard); n > 0 {
		top := g.Discard[n-1]
		v.DiscardTop = &top
		from := n - 6
		if from < 0 {
			from = 0
		}
		v.DiscardRecent = append([]Card(nil), g.Discard[from:]...)
	}
	for s := 0; s < NumSeats; s++ {
		v.HandCounts[s] = len(g.Hands[s])
	}
	for t := 0; t < 2; t++ {
		tv := TeamView{Score: g.Teams[t].Score, Down: g.Teams[t].Down(), Collector: g.Teams[t].Collector, Melds: []MeldView{}}
		for _, m := range g.Teams[t].Melds {
			mv := MeldView{ID: m.ID, Rank: m.Rank, Cards: m.Cards, By: m.By, Points: sumPoints(m.Cards)}
			tv.MeldPoints += mv.Points
			tv.Melds = append(tv.Melds, mv)
		}
		v.Teams[t] = tv
	}
	from := len(g.Log) - logToSend
	if from < 0 {
		from = 0
	}
	v.Log = append([]string{}, g.Log[from:]...)
	if seat >= 0 && seat < NumSeats {
		v.Hand = append([]Card{}, g.Hands[seat]...)
		v.CanDraw = g.CanDraw(seat)
		v.CanPickup = g.CanPickup(seat)
		if g.Phase == PhasePlaying && g.Turn == seat && g.TurnStep == StepDraw && !v.CanPickup {
			v.PickupBlocked = g.pickupBlock(seat)
			if v.PickupBlocked == "" {
				top := g.Discard[len(g.Discard)-1]
				v.PickupBlocked = fmt.Sprintf("you need two natural %ss, or a natural %s and a wild card", top.Rank, top.Rank)
			}
		}
	}
	return v
}
