package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- helpers

var testID = 10000

// c builds a card from a short code: rank + suit letter, e.g. "KS", "10H", "2C".
// "JK" is a joker.
func c(code string) Card {
	testID++
	if code == "JK" {
		return Card{ID: testID, Rank: "JOKER", Suit: "R"}
	}
	return Card{ID: testID, Rank: code[:len(code)-1], Suit: code[len(code)-1:]}
}

func cards(codes ...string) []Card {
	out := make([]Card, len(codes))
	for i, code := range codes {
		out[i] = c(code)
	}
	return out
}

// newTestGame returns a hand in progress. Dealer is West (seat 3), so North
// plays first and team 0 (North/South) counts first. It is North's turn, mid-hand.
func newTestGame() *Game {
	g := NewGame(DefaultRules())
	g.Dealer = 3
	g.Phase = PhasePlaying
	g.HandNo = 1
	g.Turn = 0
	g.TurnStep = StepDraw
	g.TurnCount = 4
	g.Stock = cards("4S", "4H", "4D", "4C", "6S", "6H", "6D", "6C")
	g.Discard = cards("9S")
	return g
}

func (g *Game) giveMeld(team int, codes ...string) *Meld {
	cs := cards(codes...)
	nat, _ := g.split(cs)
	g.nextMeldID++
	m := &Meld{ID: g.nextMeldID, Rank: nat[0].Rank, Cards: cs, By: team}
	g.Teams[team].Melds = append(g.Teams[team].Melds, m)
	if g.Teams[team].Collector < 0 {
		g.Teams[team].Collector = team
	}
	return m
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustFail(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", contains)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err, contains)
	}
}

func totalCards(g *Game) int {
	n := len(g.Stock) + len(g.Discard)
	for _, h := range g.Hands {
		n += len(h)
	}
	for _, t := range g.Teams {
		for _, m := range t.Melds {
			n += len(m.Cards)
		}
	}
	return n
}

// ---------------------------------------------------------------- tests

func TestShoe(t *testing.T) {
	shoe := newShoe()
	if len(shoe) != 108 {
		t.Fatalf("shoe has %d cards, want 108 (two decks + 4 jokers)", len(shoe))
	}
	seen := map[int]bool{}
	rankCount := map[string]int{}
	for _, c := range shoe {
		if seen[c.ID] {
			t.Fatalf("duplicate card id %d", c.ID)
		}
		seen[c.ID] = true
		rankCount[c.Rank]++
	}
	if rankCount["JOKER"] != 4 {
		t.Errorf("want 4 jokers, got %d", rankCount["JOKER"])
	}
	for _, r := range baseRanks {
		if rankCount[r] != 8 {
			t.Errorf("rank %s: want 8 cards, got %d", r, rankCount[r])
		}
	}
}

func TestCardPoints(t *testing.T) {
	want := map[string]int{
		"3S": 5, "9H": 5, "10D": 10, "JC": 10, "QH": 10, "QS": 100,
		"KS": 10, "AS": 10, "2H": 25, "JK": 50,
	}
	for code, pts := range want {
		if got := cardPoints(c(code)); got != pts {
			t.Errorf("%s: got %d points, want %d", code, got, pts)
		}
	}
}

func TestDeal(t *testing.T) {
	g := NewGame(DefaultRules())
	mustOK(t, g.StartHand())
	for s := 0; s < NumSeats; s++ {
		if len(g.Hands[s]) != 13 {
			t.Errorf("seat %d has %d cards", s, len(g.Hands[s]))
		}
	}
	if len(g.Discard) != 1 || len(g.Stock) != 55 {
		t.Errorf("discard %d / stock %d, want 1 / 55", len(g.Discard), len(g.Stock))
	}
	if g.Turn != nextSeat(g.Dealer) || g.TurnStep != StepDraw {
		t.Errorf("first turn should be the player clockwise of the dealer, drawing")
	}
	if totalCards(g) != 108 {
		t.Errorf("cards in play: %d", totalCards(g))
	}
	mustFail(t, g.StartHand(), "already in progress")
}

func TestNewMeldRules(t *testing.T) {
	cases := []struct {
		name  string
		cards []string
		err   string // "" = valid
	}{
		{"three naturals", []string{"KS", "KH", "KD"}, ""},
		{"two naturals and a wild", []string{"KS", "KH", "2C"}, ""},
		{"four naturals and a wild", []string{"KS", "KH", "KD", "KC", "2C"}, ""},
		{"three naturals and two wilds", []string{"KS", "KH", "KD", "2C", "2D"}, ""},
		{"three jokers (not wild)", []string{"JK", "JK", "JK"}, ""},
		{"jokers are not wild", []string{"KS", "KH", "JK"}, "same rank"},
		{"only two cards", []string{"KS", "KH"}, "at least 3"},
		{"one natural and two wilds", []string{"KS", "2C", "2D"}, "at least 2 natural"},
		{"all wilds", []string{"2S", "2H", "2D"}, "at least 2 natural"},
		{"two naturals and two wilds", []string{"KS", "KH", "2C", "2D"}, "outnumbered"},
		{"mixed ranks", []string{"KS", "KH", "QD"}, "same rank"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestGame()
			g.TurnStep = StepPlay
			hand := cards(tc.cards...)
			g.Hands[0] = append(hand, c("3S")) // spare card so the hand isn't emptied
			err := g.NewMeld(0, ids(hand))
			if tc.err == "" {
				mustOK(t, err)
				if !g.Teams[0].Down() {
					t.Error("team should be down after a meld")
				}
				if g.Teams[0].Collector != 0 {
					t.Error("the player who made the first meld should be the collector")
				}
			} else {
				mustFail(t, err, tc.err)
				if g.Teams[0].Down() {
					t.Error("an illegal meld must not put the team down")
				}
			}
		})
	}
}

func TestOneMeldPerRank(t *testing.T) {
	g := newTestGame()
	g.TurnStep = StepPlay
	g.giveMeld(0, "KS", "KH", "KD")
	g.Hands[0] = cards("KC", "KC", "KS", "3S")
	mustFail(t, g.NewMeld(0, ids(g.Hands[0][:3])), "already has a meld")
}

func TestAddToMeld(t *testing.T) {
	g := newTestGame()
	g.TurnStep = StepPlay
	m := g.giveMeld(0, "KS", "KH", "KD")
	g.Turn = 2 // South, North's partner, may add to the team's melds
	g.Hands[2] = cards("KC", "2C", "2D", "2H", "QS", "3S")

	mustFail(t, g.AddToMeld(2, m.ID, ids(g.Hands[2][4:5])), "only Ks and wild")
	mustOK(t, g.AddToMeld(2, m.ID, ids(g.Hands[2][0:1]))) // natural K
	mustOK(t, g.AddToMeld(2, m.ID, ids(g.Hands[2][0:1]))) // wild: 4 naturals, 1 wild
	mustOK(t, g.AddToMeld(2, m.ID, ids(g.Hands[2][0:1]))) // 4 naturals, 2 wilds
	mustOK(t, g.AddToMeld(2, m.ID, ids(g.Hands[2][0:1]))) // 4 naturals, 3 wilds
	if len(m.Cards) != 7 {
		t.Fatalf("meld has %d cards", len(m.Cards))
	}
	g.Hands[2] = append(g.Hands[2], c("2S"))
	mustFail(t, g.AddToMeld(2, m.ID, ids(g.Hands[2][len(g.Hands[2])-1:])), "outnumbered")

	// the other team can't touch it
	g.Turn = 1
	g.Hands[1] = cards("KC", "3S")
	mustFail(t, g.AddToMeld(1, m.ID, ids(g.Hands[1][:1])), "not one of your team")
}

func TestTurnOrderAndPhases(t *testing.T) {
	g := newTestGame()
	g.Hands[0] = cards("3S", "5H")
	mustFail(t, g.Draw(1), "not your turn")
	mustFail(t, g.DiscardCard(0, g.Hands[0][0].ID), "draw a card")
	mustFail(t, g.NewMeld(0, nil), "draw a card")
	mustOK(t, g.Draw(0))
	mustFail(t, g.Draw(0), "already drawn")
	mustOK(t, g.DiscardCard(0, g.Hands[0][0].ID))
	if g.Turn != 1 || g.TurnStep != StepDraw {
		t.Fatalf("turn should pass clockwise to East, got seat %d step %s", g.Turn, g.TurnStep)
	}
	// clockwise order from above: N -> E -> S -> W -> N
	for _, want := range []int{2, 3, 0} {
		g.Hands[g.Turn] = cards("3S", "5H", "7D")
		mustOK(t, g.Draw(g.Turn))
		mustOK(t, g.DiscardCard(g.Turn, g.Hands[g.Turn][0].ID))
		if g.Turn != want {
			t.Fatalf("expected seat %d, got %d", want, g.Turn)
		}
	}
}

func TestFirstPlayerMustDraw(t *testing.T) {
	g := NewGame(DefaultRules())
	mustOK(t, g.StartHand())
	seat := g.Turn
	top := g.Discard[0]
	// give the first player a perfect pickup hand; it must still be refused
	g.Hands[seat] = append(cards("3S"), Card{ID: 20001, Rank: top.Rank, Suit: "S"}, Card{ID: 20002, Rank: top.Rank, Suit: "H"})
	if top.Rank == "2" || top.Rank == "JOKER" {
		return // a wild on top can't be picked up anyway
	}
	if g.CanPickup(seat) {
		t.Fatal("first player must not be able to take the pile")
	}
	mustFail(t, g.Pickup(seat, nil), "must draw")
	mustOK(t, g.Draw(seat))
}

func TestPickup(t *testing.T) {
	t.Run("team must be down", func(t *testing.T) {
		g := newTestGame()
		g.Hands[0] = cards("9H", "9D", "3S")
		if g.CanPickup(0) {
			t.Fatal("team not down: pickup should be unavailable")
		}
		mustFail(t, g.Pickup(0, nil), "must be down")
	})
	t.Run("two naturals", func(t *testing.T) {
		g := newTestGame()
		g.giveMeld(0, "KS", "KH", "KD")
		g.Discard = cards("3C", "8D", "9S")
		g.Hands[0] = cards("9H", "9D", "QS")
		if !g.CanPickup(0) {
			t.Fatal("should be able to pick up")
		}
		before := totalCards(g)
		mustOK(t, g.Pickup(0, nil))
		if len(g.Discard) != 0 {
			t.Error("discard pile should be empty")
		}
		if m := g.Teams[0].meldFor("9"); m == nil || len(m.Cards) != 3 {
			t.Error("the top card and two 9s should form a new meld")
		}
		if len(g.Hands[0]) != 3 { // QS + 3C + 8D
			t.Errorf("hand has %d cards, want 3", len(g.Hands[0]))
		}
		if g.TurnStep != StepPlay {
			t.Error("player should now be in the play step")
		}
		if totalCards(g) != before {
			t.Error("cards were lost or created")
		}
	})
	t.Run("one natural and a wild", func(t *testing.T) {
		g := newTestGame()
		g.giveMeld(0, "KS", "KH", "KD")
		g.Hands[0] = cards("9H", "2D", "QS")
		mustOK(t, g.Pickup(0, nil))
		if m := g.Teams[0].meldFor("9"); m == nil || len(m.Cards) != 3 {
			t.Error("expected a 9 meld using one wild")
		}
	})
	t.Run("not enough", func(t *testing.T) {
		g := newTestGame()
		g.giveMeld(0, "KS", "KH", "KD")
		g.Hands[0] = cards("9H", "QS", "3C")
		if g.CanPickup(0) {
			t.Fatal("one natural alone is not enough")
		}
		mustFail(t, g.Pickup(0, nil), "two natural 9s")
	})
	t.Run("two wilds and one natural is not enough", func(t *testing.T) {
		g := newTestGame()
		g.giveMeld(0, "KS", "KH", "KD")
		g.Hands[0] = cards("9H", "2D", "2C")
		g.Discard = cards("9S")
		mustFail(t, g.Pickup(0, ids(g.Hands[0])), "outnumbered")
	})
	t.Run("blocked when your team already has that rank", func(t *testing.T) {
		g := newTestGame()
		g.giveMeld(0, "9C", "9D", "9H")
		g.Hands[0] = cards("9H", "9D", "QS")
		if g.CanPickup(0) {
			t.Fatal("pickup should be blocked: team already has a 9 meld")
		}
		mustFail(t, g.Pickup(0, nil), "blocked")
	})
	t.Run("a wild card on top blocks the pile", func(t *testing.T) {
		g := newTestGame()
		g.giveMeld(0, "KS", "KH", "KD")
		g.Discard = cards("3C", "2S")
		g.Hands[0] = cards("2H", "2D", "QS")
		if g.CanPickup(0) {
			t.Fatal("wild on top should block the pile")
		}
	})
	t.Run("pickup rule can be relaxed", func(t *testing.T) {
		g := newTestGame()
		g.Rules.PickupRequiresDown = false
		g.Hands[0] = cards("9H", "9D", "3S")
		mustOK(t, g.Pickup(0, nil))
	})
	t.Run("chosen cards are validated", func(t *testing.T) {
		g := newTestGame()
		g.giveMeld(0, "KS", "KH", "KD")
		g.Hands[0] = cards("9H", "QS", "9D")
		mustFail(t, g.Pickup(0, ids(g.Hands[0][:2])), "same rank")
	})
}

func TestGoOutByDiscardAndScoring(t *testing.T) {
	g := newTestGame() // team 0 counts first (dealer is West)
	g.TurnStep = StepPlay
	g.giveMeld(0, "KS", "KH", "KD") // 30
	g.giveMeld(1, "5S", "5H", "5D") // 15
	g.Hands[0] = cards("3S")
	g.Hands[2] = cards("5C")       // partner's leftover card is not deducted from the winner
	g.Hands[1] = cards("QS")       // 100
	g.Hands[3] = cards("JK", "2H") // 50 + 25
	g.Teams[0].Score = 100
	g.Teams[1].Score = 100

	mustOK(t, g.DiscardCard(0, g.Hands[0][0].ID))
	if g.Phase != PhaseHandOver {
		t.Fatalf("phase %s, want handover", g.Phase)
	}
	r := g.Result
	if r.Winner != 0 || r.OutSeat != 0 || r.FirstCount != 0 {
		t.Fatalf("bad result header: %+v", r)
	}
	if r.Teams[0].Delta != 30 || g.Teams[0].Score != 130 {
		t.Errorf("winner: delta %d score %d, want 30 / 130", r.Teams[0].Delta, g.Teams[0].Score)
	}
	want := 15 - (100 + 50 + 25)
	if r.Teams[1].Delta != want || g.Teams[1].Score != 100+want {
		t.Errorf("loser: delta %d score %d, want %d / %d", r.Teams[1].Delta, g.Teams[1].Score, want, 100+want)
	}
	// next hand: the dealer moves clockwise and the other team counts first
	mustOK(t, g.StartHand())
	if g.Dealer != 0 || g.FirstCountTeam() != 1 {
		t.Errorf("dealer %d firstCount %d, want 0 / 1", g.Dealer, g.FirstCountTeam())
	}
}

func TestGoOutByMelding(t *testing.T) {
	g := newTestGame()
	g.TurnStep = StepPlay
	g.giveMeld(0, "KS", "KH", "KD")
	g.Hands[0] = cards("9S", "9H", "9D")
	mustOK(t, g.NewMeld(0, ids(g.Hands[0])))
	if g.Phase != PhaseHandOver || g.Result.OutSeat != 0 {
		t.Fatal("melding the last card should go out")
	}
}

func TestNonDealingTeamCountsFirstAndWinsTies(t *testing.T) {
	for _, dealer := range []int{0, 1, 2, 3} {
		g := newTestGame()
		g.Dealer = dealer
		first := g.FirstCountTeam()
		if first == teamOf(dealer) {
			t.Fatalf("dealer %d: the dealing team must not count first", dealer)
		}
		g.TurnStep = StepPlay
		// the team that did not deal and the dealing team both finish above the target
		g.giveMeld(0, "KS", "KH", "KD")
		g.giveMeld(1, "KS", "KH", "KD")
		g.Teams[0].Score = 1990
		g.Teams[1].Score = 1990
		for s := range g.Hands {
			g.Hands[s] = nil
		}
		g.Turn = first // the first-counting team goes out, so both teams add 30
		g.Hands[first] = cards("3S")
		mustOK(t, g.DiscardCard(first, g.Hands[first][0].ID))
		if g.Phase != PhaseGameOver || g.Result.GameWinner != first {
			t.Errorf("dealer %d: the non-dealing team (%d) should win the tie, got winner %d", dealer, first, g.Result.GameWinner)
		}
	}
}

func TestDealingTeamCanWinWhenOtherTeamDoesNot(t *testing.T) {
	g := newTestGame() // dealer West -> team 1 deals, team 0 counts first
	g.TurnStep = StepPlay
	g.giveMeld(1, "KS", "KH", "KD")
	g.Teams[0].Score = 0
	g.Teams[1].Score = 1980
	g.Turn = 1
	g.Hands[1] = cards("3S")
	g.Hands[0], g.Hands[2], g.Hands[3] = nil, nil, nil
	mustOK(t, g.DiscardCard(1, g.Hands[1][0].ID))
	if g.Phase != PhaseGameOver || g.Result.GameWinner != 1 {
		t.Fatalf("team 1 should have won the game: %+v", g.Result)
	}
}

func TestStockRunsOut(t *testing.T) {
	g := newTestGame()
	g.Stock = nil
	g.TurnStep = StepPlay
	g.giveMeld(0, "KS", "KH", "KD") // 30
	g.Hands[0] = cards("3S", "4S")  // 5 left after discard
	g.Hands[1] = cards("QS")        // 100
	mustOK(t, g.DiscardCard(0, g.Hands[0][1].ID))
	if g.Phase != PhaseHandOver {
		t.Fatalf("hand should end when the stock is empty and nobody can take the pile (phase %s)", g.Phase)
	}
	if g.Result.Winner != -1 || g.Result.OutSeat != -1 {
		t.Error("nobody went out")
	}
	if g.Result.Teams[0].Delta != 30-5 || g.Result.Teams[1].Delta != -100 {
		t.Errorf("both teams should deduct their hands: %+v", g.Result.Teams)
	}
}

func TestStockEmptyButPileAvailable(t *testing.T) {
	g := newTestGame()
	g.Stock = nil
	g.TurnStep = StepPlay
	g.giveMeld(1, "KS", "KH", "KD") // East/West are down
	g.Hands[0] = cards("3S", "9C")
	g.Hands[1] = cards("9H", "9D", "QS")
	mustOK(t, g.DiscardCard(0, g.Hands[0][1].ID)) // 9C on top; East holds two 9s
	if g.Phase != PhasePlaying {
		t.Fatal("East can take the pile, so the hand must continue")
	}
	mustFail(t, g.Draw(1), "stock is empty")
	mustOK(t, g.Pickup(1, nil))
}

func TestViewDoesNotLeakHands(t *testing.T) {
	g := NewGame(DefaultRules())
	mustOK(t, g.StartHand())
	spec := g.View(-1)
	if len(spec.Hand) != 0 {
		t.Fatal("spectators must not receive any hand")
	}
	b, _ := json.Marshal(spec)
	if strings.Contains(string(b), `"hand"`) {
		t.Fatal("spectator JSON contains a hand field")
	}
	v := g.View(2)
	if len(v.Hand) != 13 {
		t.Fatalf("player should see their own 13 cards, got %d", len(v.Hand))
	}
	mine := map[int]bool{}
	for _, c := range v.Hand {
		mine[c.ID] = true
	}
	b, _ = json.Marshal(v)
	var raw struct {
		Hand []Card `json:"hand"`
	}
	json.Unmarshal(b, &raw)
	for _, c := range g.Hands[0] {
		if mine[c.ID] {
			t.Fatal("hands overlap")
		}
	}
	if v.HandCounts != [4]int{13, 13, 13, 13} {
		t.Errorf("hand counts %v", v.HandCounts)
	}
}

// Full games between bots: no illegal state, no lost cards, always terminates.
func TestBotsPlayCompleteGames(t *testing.T) {
	for game := 0; game < 25; game++ {
		rules := DefaultRules()
		rules.TargetScore = 800
		g := NewGame(rules)
		hands := 0
		for g.Phase != PhaseGameOver {
			if hands++; hands > 400 {
				t.Fatalf("game %d did not finish in 400 hands (scores %d / %d)", game, g.Teams[0].Score, g.Teams[1].Score)
			}
			mustOK(t, g.StartHand())
			turns := 0
			for g.Phase == PhasePlaying {
				if turns++; turns > 400 {
					t.Fatalf("game %d hand %d did not finish", game, hands)
				}
				seat := g.Turn
				botPlay(g, seat)
				if n := totalCards(g); n != 108 {
					t.Fatalf("game %d: %d cards in play after seat %d's turn", game, n, seat)
				}
				if g.Phase == PhasePlaying && g.Turn == seat {
					t.Fatalf("game %d: bot at seat %d did not complete its turn", game, seat)
				}
			}
			if g.Result == nil {
				t.Fatal("hand ended without a result")
			}
		}
		if g.Result.GameWinner < 0 {
			t.Fatal("game over without a winner")
		}
	}
}

func TestPickupThatEmptiesTheHandGoesOut(t *testing.T) {
	g := newTestGame()
	g.giveMeld(0, "KS", "KH", "KD")
	g.Discard = cards("9S") // pile is just the top card
	g.Hands[0] = cards("9H", "9D")
	mustOK(t, g.Pickup(0, nil))
	if g.Phase != PhaseHandOver || g.Result.OutSeat != 0 {
		t.Fatalf("taking the pile with every card in hand should go out (phase %s)", g.Phase)
	}
}

func TestSetHandOrder(t *testing.T) {
	g := newTestGame()
	g.Hands[0] = cards("7S", "8H", "KD")
	ids := []int{g.Hands[0][2].ID, g.Hands[0][0].ID, g.Hands[0][1].ID}
	mustOK(t, g.SetHandOrder(0, ids))
	if g.Hands[0][0].Rank != "K" || g.Hands[0][1].Rank != "7" || g.Hands[0][2].Rank != "8" {
		t.Fatalf("hand not re-ordered: %v", g.Hands[0])
	}
	mustFail(t, g.SetHandOrder(0, ids[:2]), "whole hand")
	mustFail(t, g.SetHandOrder(0, []int{ids[0], ids[0], ids[1]}), "not in your hand")
	mustFail(t, g.SetHandOrder(0, []int{ids[0], ids[1], 9999}), "not in your hand")
	mustFail(t, g.SetHandOrder(-1, ids), "not seated")
}

func TestViewWithEmptyDiscardPileIsNotNull(t *testing.T) {
	g := newTestGame()
	g.Discard = nil
	v := g.View(0)
	if v.DiscardRecent == nil || v.DiscardTop != nil {
		t.Fatalf("empty pile should give an empty (non-nil) recent list and no top card: %+v", v)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"discardRecent":[]`) {
		t.Fatalf("discardRecent must serialise as [] not null: %s", b)
	}
}

func TestJokersNotWildByDefault(t *testing.T) {
	if DefaultRules().JokersWild {
		t.Fatal("jokers must not be wild by default")
	}
	g := newTestGame()
	g.giveMeld(0, "KS", "KH", "KD")
	g.Hands[0] = cards("9H", "JK", "QS")
	if g.CanPickup(0) {
		t.Fatal("a joker must not count as a wild card for taking the pile")
	}
}

func TestScoreAccumulatesAcrossHands(t *testing.T) {
	g := NewGame(DefaultRules())
	for hand := 1; hand <= 3; hand++ {
		mustOK(t, g.StartHand())
		g.TurnStep = StepPlay
		g.giveMeld(0, "KS", "KH", "KD") // 30 for team 0
		for s := range g.Hands {
			g.Hands[s] = nil
		}
		g.Turn = 0
		g.Hands[0] = cards("3S")
		mustOK(t, g.DiscardCard(0, g.Hands[0][0].ID))
		if g.Phase != PhaseHandOver {
			t.Fatalf("hand %d: game should continue (phase %s)", hand, g.Phase)
		}
		if want := 30 * hand; g.Teams[0].Score != want {
			t.Fatalf("after hand %d team 0 has %d, want %d", hand, g.Teams[0].Score, want)
		}
	}
}

func newJokerGame() *Game {
	g := newTestGame()
	g.Rules.JokersWild = true
	return g
}

func TestJokersWildOnceDown(t *testing.T) {
	t.Run("joker cannot start the first meld", func(t *testing.T) {
		g := newJokerGame()
		g.Turn, g.TurnStep = 0, StepPlay
		g.Hands[0] = cards("KS", "KH", "JK", "3C")
		mustFail(t, g.NewMeld(0, ids(g.Hands[0][:3])), "once your team is down")
	})
	t.Run("joker can be played in a new meld when down", func(t *testing.T) {
		g := newJokerGame()
		g.giveMeld(0, "QS", "QH", "QD")
		g.Turn, g.TurnStep = 0, StepPlay
		g.Hands[0] = cards("KS", "KH", "JK", "3C")
		mustOK(t, g.NewMeld(0, ids(g.Hands[0][:3])))
	})
	t.Run("joker can be added to an existing meld", func(t *testing.T) {
		g := newJokerGame()
		m := g.giveMeld(0, "QS", "QH", "QD")
		g.Turn, g.TurnStep = 0, StepPlay
		g.Hands[0] = cards("JK", "3C")
		mustOK(t, g.AddToMeld(0, m.ID, ids(g.Hands[0][:1])))
	})
	t.Run("natural plus joker takes the pile when down", func(t *testing.T) {
		g := newJokerGame()
		g.giveMeld(0, "KS", "KH", "KD")
		g.Hands[0] = cards("9H", "JK", "QS")
		mustOK(t, g.Pickup(0, nil))
		if m := g.Teams[0].meldFor("9"); m == nil || len(m.Cards) != 3 {
			t.Error("expected a 9 meld using the joker")
		}
	})
	t.Run("joker on top blocks the pile", func(t *testing.T) {
		g := newJokerGame()
		g.giveMeld(0, "KS", "KH", "KD")
		g.Discard = cards("3C", "JK")
		g.Hands[0] = cards("2H", "2D", "QS")
		if g.CanPickup(0) {
			t.Fatal("a joker on top should block the pile")
		}
	})
}
