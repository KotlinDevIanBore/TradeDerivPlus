// Command wscheck verifies the three WebSocket endpoints against a running API.
// It registers a throwaway user, then listens on prices, signals and account.
//
//	go run ./scripts/wscheck [baseHTTP] [seconds]
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	base := "http://localhost:8080/api/v1"
	if len(os.Args) > 1 {
		base = os.Args[1]
	}
	seconds := 12
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil {
			seconds = n
		}
	}
	wsBase := "ws" + strings.TrimPrefix(strings.TrimSuffix(base, "/"), "http")

	token, accountID, err := register(base)
	if err != nil {
		fmt.Println("FAIL: could not register:", err)
		os.Exit(1)
	}
	fmt.Printf("registered throwaway user, account %s\n\n", accountID)

	var wg sync.WaitGroup
	counts := struct {
		sync.Mutex
		m map[string]int
	}{m: map[string]int{}}

	listen := func(label, url string) {
		defer wg.Done()

		conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			fmt.Printf("  [FAIL] %-8s dial failed (http %d): %v\n", label, status, err)
			return
		}
		defer conn.Close()

		deadline := time.Now().Add(time.Duration(seconds) * time.Second)
		_ = conn.SetReadDeadline(deadline)

		var firstSample string
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				break
			}
			counts.Lock()
			counts.m[label]++
			if firstSample == "" {
				firstSample = string(raw)
				if len(firstSample) > 130 {
					firstSample = firstSample[:130] + "..."
				}
			}
			counts.Unlock()
		}

		counts.Lock()
		n := counts.m[label]
		counts.Unlock()

		mark := "PASS"
		if n == 0 && label != "signals" {
			mark = "FAIL"
		}
		fmt.Printf("  [%s] %-8s %3d messages\n", mark, label, n)
		if firstSample != "" {
			fmt.Printf("           first: %s\n", firstSample)
		}
	}

	fmt.Printf("listening for %ds...\n", seconds)
	wg.Add(3)
	go listen("prices", fmt.Sprintf("%s/ws/prices?symbols=R_75,BOOM500&token=%s", wsBase, token))
	go listen("signals", fmt.Sprintf("%s/ws/signals?token=%s", wsBase, token))
	go listen("account", fmt.Sprintf("%s/ws/account?token=%s", wsBase, token))

	// Trigger an account-topic push so that socket has something to report.
	go func() {
		time.Sleep(3 * time.Second)
		body := fmt.Sprintf(`{"account_id":%q,"symbol":"R_75","direction":"buy","stake":"25"}`, accountID)
		post(base+"/trades", token, body)
	}()

	wg.Wait()

	// An unauthenticated socket must be rejected.
	if _, resp, err := websocket.DefaultDialer.Dial(wsBase+"/ws/prices?symbols=R_75", nil); err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		if status == http.StatusUnauthorized {
			fmt.Println("  [PASS] socket without a token is rejected (401)")
		} else {
			fmt.Printf("  [FAIL] socket without a token got http %d, want 401\n", status)
		}
	} else {
		fmt.Println("  [FAIL] socket without a token was accepted")
	}
}

func register(base string) (token, accountID string, err error) {
	phone := fmt.Sprintf("07%08d", rand.Intn(100000000))
	body := fmt.Sprintf(`{"phone_number":%q,"password":"hunter2024"}`, phone)

	raw, err := post(base+"/auth/register", "", body)
	if err != nil {
		return "", "", err
	}
	// Round 2: the access token is a top-level field and the refresh token
	// is a cookie, so there is no token object to unwrap.
	var out struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("%w (body %s)", err, raw)
	}
	return out.AccessToken, out.Account.ID, nil
}

func post(url, token, body string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
