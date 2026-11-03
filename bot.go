package main

// A simple computer player so one person can try the game alone (or the
// spectator screen can run a demo). It only uses the public Game moves, so it
// can never cheat or make an illegal play.

import "sort"

// botPlay performs one complete turn for the seat.
func botPlay(g *Game, seat int) {
	if g.Phase != PhasePlaying || g.Turn != seat {
		return
	}
	// 1. Draw: take the pile when allowed, otherwise draw from the stock.
	if g.TurnStep == StepDraw {
		if g.CanPickup(seat) {
			if err := g.Pickup(seat, nil); err != nil {
				g.Draw(seat)
			}
		} else if err := g.Draw(seat); err != nil {
			return
		}
	}
	// 2. Meld and add as much as possible.
	for g.Phase == PhasePlaying && g.Turn == seat && g.TurnStep == StepPlay && botPlayStep(g, seat) {
	}
	// 3. Discard (unless melding already took the last card and ended the hand).
	if g.Phase == PhasePlaying && g.Turn == seat && g.TurnStep == StepPlay && len(g.Hands[seat]) > 0 {
		g.DiscardCard(seat, botChooseDiscard(g, seat))
	}
}

func ids(cards []Card) []int {
	out := make([]int, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}

// botPlayStep makes one meld/add if it can and reports whether it did.
func botPlayStep(g *Game, seat int) bool {
	team := g.Teams[teamOf(seat)]
	byRank := map[string][]Card{}
	var wilds []Card
	for _, c := range g.Hands[seat] {
		if g.isWild(c) {
			if c.Rank == "JOKER" && !team.Down() {
				continue // not playable until the team is down
			}
			wilds = append(wilds, c)
		} else {
			byRank[c.Rank] = append(byRank[c.Rank], c)
		}
	}
	ranks := make([]string, 0, len(byRank))
	for r := range byRank {
		ranks = append(ranks, r)
	}
	sort.Strings(ranks)

	// Add naturals to existing melds.
	for _, m := range team.Melds {
		if cs := byRank[m.Rank]; len(cs) > 0 {
			if g.AddToMeld(seat, m.ID, ids(cs)) == nil {
				return true
			}
		}
	}
	// New meld from three or more naturals.
	for _, r := range ranks {
		if cs := byRank[r]; len(cs) >= 3 && team.meldFor(r) == nil {
			if g.NewMeld(seat, ids(cs)) == nil {
				return true
			}
		}
	}
	// New meld from two naturals and a wild card.
	if len(wilds) > 0 {
		for _, r := range ranks {
			if cs := byRank[r]; len(cs) == 2 && team.meldFor(r) == nil {
				if g.NewMeld(seat, append(ids(cs), wilds[0].ID)) == nil {
					return true
				}
			}
		}
	}
	// add jokers
	// are there any already down?
	// or do I create a new meld
	if cs := byRank["JOKER"]; len(cs) > 0 && team.meldFor("JOKER") == nil {
		if g.NewMeld(seat, ids(cs)) == nil {
			return true
		}
	}
	// Spare wild cards go onto melds that have plenty of naturals.
	if len(wilds) > 0 {
		for _, m := range team.Melds {
			nat, w := g.split(m.Cards)
			if len(w)+1 < len(nat) {
				if g.AddToMeld(seat, m.ID, []int{wilds[0].ID}) == nil {
					return true
				}
			}
		}
	}
	return false
}

// botChooseDiscard keeps wild cards and pairs, and gets rid of lone high-value cards first.
func botChooseDiscard(g *Game, seat int) int {
	count := map[string]int{}
	for _, c := range g.Hands[seat] {
		count[c.Rank]++
	}
	best, bestKeep := g.Hands[seat][0], 1<<30
	for _, c := range g.Hands[seat] {
		keep := count[c.Rank]*40 - cardPoints(c)/5
		if g.isWild(c) {
			keep += 1000
		}
		if keep < bestKeep {
			best, bestKeep = c, keep
		}
	}
	return best.ID
}
