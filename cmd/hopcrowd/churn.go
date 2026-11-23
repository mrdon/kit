package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mrdon/kit/internal/apps/hoppers"
)

// churn tests the two ways a phone comes back, once round 2 is racing:
//   - "reconnect": phone A's connection dies for longer than the engine's
//     seen window (so it is marked gone), then it reconnects with the SAME
//     cookie jar and must still be the same frog.
//   - "reclaim": phone B is lost outright (cookie jar thrown away); the host
//     issues a code for its frog, a new device redeems it, and must get the
//     same frog and points. The code must then be burned.
//
// Results go to the log as "CHURN ok ..." or "CHURN FAIL ...".
func churn(ctx context.Context, a, b *phone, away time.Duration, host *conn) {
	idA, _ := a.snapshot()
	idB, scoreB := b.snapshot()
	a.log("CHURN drop phone %d (%s, %s) and lose phone %d (%s, %s, %d pts)", a.n, a.name, idA, b.n, b.name, idB, scoreB)
	a.drop()
	b.drop()
	select {
	case <-ctx.Done():
		return
	case <-time.After(away):
	}

	if conn, err := connected(ctx, a.c, idA); err == nil {
		a.log("CHURN while away: %s connected=%v (want false: past the seen window)", idA, conn)
	}
	a.listen(ctx) // same jar: the cookie is still there
	time.Sleep(2 * time.Second)
	if got, _ := a.snapshot(); got == idA {
		a.log("CHURN ok reconnect: phone %d is still %s", a.n, got)
	} else {
		a.log("CHURN FAIL reconnect: phone %d was %s, now %s", a.n, idA, got)
	}
	reclaim(ctx, b, idB, scoreB, host)
}

func reclaim(ctx context.Context, b *phone, idB string, scoreB int, host *conn) {
	var issued struct {
		Code string `json:"code"`
	}
	if _, err := host.post(ctx, "/players/"+idB+"/reclaim", nil, &issued); err != nil {
		b.log("CHURN FAIL reclaim: the host could not issue a code: %v", err)
		return
	}
	b.c.client = newJar() // a brand-new device
	body := map[string]string{"playerId": idB, "code": issued.Code}
	if _, err := b.c.post(ctx, "/reclaim", body, nil); err != nil {
		b.log("CHURN FAIL reclaim: %v", err)
		return
	}
	b.listen(ctx)
	time.Sleep(2 * time.Second)
	id, score := b.snapshot()
	switch {
	case id != idB:
		b.log("CHURN FAIL reclaim: wanted %s, got %s", idB, id)
	case score < scoreB:
		b.log("CHURN FAIL reclaim: %s had %d pts, now %d", idB, scoreB, score)
	default:
		b.log("CHURN ok reclaim: phone %d is %s again with %d pts (was %d)", b.n, id, score, scoreB)
	}
	if _, err := newConnLike(b.c).post(ctx, "/reclaim", body, nil); err == nil {
		b.log("CHURN FAIL reclaim: the code %s worked twice", issued.Code)
	} else {
		b.log("CHURN ok reclaim: the code is burned (%s)", fmt.Sprint(err))
	}
}

// newConnLike is another device on the same surface, with its own empty jar.
func newConnLike(c *conn) *conn { return &conn{base: c.base, client: newJar()} }

// connected asks the TV view whether a player is connected.
func connected(ctx context.Context, c *conn, id string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/tv/state", nil)
	if err != nil {
		return false, fmt.Errorf("building state: %w", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("getting state: %w", err)
	}
	defer res.Body.Close()
	var f hoppers.Frame
	if err := json.NewDecoder(res.Body).Decode(&f); err != nil {
		return false, fmt.Errorf("decoding state: %w", err)
	}
	for _, p := range f.Players {
		if p.ID == id {
			return p.Connected, nil
		}
	}
	return false, fmt.Errorf("no player %s", id)
}
