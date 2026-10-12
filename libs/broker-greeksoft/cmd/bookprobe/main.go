// bookprobe prints the broker's own REST order book and, for one order,
// its order detail and trade detail -- to see exactly what GreekSoft stores
// (tags included).
//
// READ-ONLY and LOGIN-FREE: it takes the session the running platform
// already saved in Postgres (broker_sessions) and exits if there is none.
// There is no login call in this program, so it cannot log the platform's
// websockets out.
//
//	go run ./cmd/bookprobe [gorderid] [rows]
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	greeksoft "trading-platform/libs/broker-greeksoft"
	broker "trading-platform/libs/go-broker"
)

func env(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		log.Fatalf("%s is empty", name)
	}
	return v
}

func show(title string, v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Printf("\n===== %s =====\n%s\n", title, b)
}

func main() {
	account := strings.ToUpper(strings.TrimSpace(os.Getenv("GREEK_ACCOUNT_ID")))
	if account == "" {
		log.Fatal("GREEK_ACCOUNT_ID is empty")
	}
	db, err := sql.Open("postgres", env("POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	var token string
	var state sql.NullString
	var raw []byte
	var updated time.Time
	err = db.QueryRowContext(ctx, `SELECT session_token, session_state, broker_specific, updated_at
		FROM broker_sessions WHERE broker = 'greeksoft' AND user_id = $1`, account).Scan(&token, &state, &raw, &updated)
	if err != nil || token == "" || (state.Valid && state.String == "INVALIDATED") {
		log.Fatalf("no usable shared session for account %s (err=%v) -- not logging in, exiting", account, err)
	}
	spec := map[string]interface{}{}
	_ = json.Unmarshal(raw, &spec)
	c := greeksoft.NewClient(env("GREEK_API_AUTH_URL"), env("GREEK_API_REST_URL"))
	c.Session = &broker.SessionDetails{UserID: account, AuthToken: token, IsLoggedIn: true, BrokerSpecific: spec}
	fmt.Printf("session saved at %s, reused without login\n", updated.Format("15:04:05"))

	// bookprobe ohlc <gtoken>...: the last 1-minute candles of each token.
	if len(os.Args) > 2 && os.Args[1] == "ohlc" {
		for _, t := range os.Args[2:] {
			g, _ := strconv.ParseInt(t, 10, 64)
			resp, err := c.GetOHLC(ctx, g, 1, time.Now().Format("20060102"), 1)
			if err != nil {
				fmt.Printf("%s: %v\n", t, err)
				continue
			}
			r, _ := resp["response"].(map[string]interface{})
			d, _ := r["data"].(map[string]interface{})
			bars, _ := d["data"].([]interface{})
			fmt.Printf("%s: %d bars\n", t, len(bars))
			// In the order the broker sent them: first 2, then the last 8.
			for i, b := range bars {
				if i >= 2 && i < len(bars)-8 {
					continue
				}
				m, _ := b.(map[string]interface{})
				ts, _ := strconv.ParseFloat(fmt.Sprintf("%v", m["timestamp"]), 64)
				fmt.Printf("   #%d %s close=%v\n", i, time.Unix(int64(ts), 0).Format("02 Jan 15:04:05"), m["Close"])
			}
			today, latest := 0, int64(0)
			var latestClose interface{}
			for _, b := range bars {
				m, _ := b.(map[string]interface{})
				ts, _ := strconv.ParseFloat(fmt.Sprintf("%v", m["timestamp"]), 64)
				if time.Unix(int64(ts), 0).Format("20060102") == time.Now().Format("20060102") {
					today++
				}
				if int64(ts) > latest {
					latest, latestClose = int64(ts), m["Close"]
				}
			}
			fmt.Printf("   today's bars: %d, newest %s close=%v\n", today, time.Unix(latest, 0).Format("15:04:05"), latestClose)
		}
		return
	}

	rows := 3
	if len(os.Args) > 2 {
		rows, _ = strconv.Atoi(os.Args[2])
	}
	book, err := c.GetOrderBook(ctx)
	if err != nil {
		log.Fatalf("order book: %v", err)
	}
	// Print the first rows of whatever list the reply carries.
	var list []interface{}
	var find func(v interface{})
	find = func(v interface{}) {
		if list != nil {
			return
		}
		switch n := v.(type) {
		case []interface{}:
			if len(n) > 0 {
				if _, ok := n[0].(map[string]interface{}); ok {
					list = n
					return
				}
			}
		case map[string]interface{}:
			for _, x := range n {
				find(x)
			}
		}
	}
	find(book)
	fmt.Printf("order book rows: %d\n", len(list))
	for i := 0; i < len(list) && i < rows; i++ {
		show(fmt.Sprintf("ORDER BOOK row %d", i+1), list[i])
	}
	if len(list) > rows {
		show("ORDER BOOK last row", list[len(list)-1])
	}

	if len(os.Args) > 1 {
		id, gscid := os.Args[1], account // the saved session has no gscid; it equals the account id
		if d, err := c.GetOrderDetail(ctx, id, gscid); err != nil {
			fmt.Printf("\norder detail %s: %v\n", id, err)
		} else {
			show("ORDER DETAIL "+id, d)
		}
		if d, err := c.GetTradeDetail(ctx, id, gscid); err != nil {
			fmt.Printf("\ntrade detail %s: %v\n", id, err)
		} else {
			show("TRADE DETAIL "+id, d)
		}
	}
}
