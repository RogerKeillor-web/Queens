# Queens

A four-player partnership card game. Go server, JavaScript browser clients, talking over WebSockets.

    go run .                 # then open http://localhost:8080
    go run . -addr :9000 -bot-delay 800ms -target 2000
    go test -race ./...

- `/table`  spectator view (TV / laptop): both teams' melds, stock, discard, scores, whose turn, next dealer, which team counts first.
- `/play`   player view (phone / tablet / desktop): your hand, stock, discard, team melds, auto-sort toggle, drag-to-reorder (touch or mouse).
- The table screen has a lobby: seat players, "Fill empty seats with bots", "Deal first hand". Bots let one person try the game alone.

The WebSocket layer (`ws.go`) is a small dependency-free RFC 6455 implementation, so the server builds with the standard library only and the web files are embedded in the binary.

## Rule decisions (where the notes were open)

| Topic | Decision | Change with |
|---|---|---|
| Wild cards | Only 2s. Jokers are not wild (they are their own rank: 3 jokers make a meld) | `-jokers-wild` makes jokers wild once the team is down |
| Meld | 3+ cards of one rank, naturals must outnumber wilds; one meld per rank per team | |
| Taking the pile | Team must already be down; need 2 naturals of the top rank, or 1 natural + 1 wild; a wild on top or a rank the team already melded blocks it | `-pickup-requires-down=false` |
| First player of a hand | Must draw from the stock | |
| Going out | Empty your hand (by melding or discarding) | |
| Scoring | Winners add melds; losers add melds and subtract hand; non-dealing team counts first | |
| Target | Scores accumulate over as many hands as it takes; first team to 2000 wins the game | `-target` |
| Instant 100-point deal bonus | Not implemented (flag reserved). The notes are ambiguous about what hand triggers it | |
| 6 players | Not yet; seats and teams are modelled for 4 | |

Hand reordering is private to each player and is not broadcast.
