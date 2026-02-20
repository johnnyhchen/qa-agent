// Package main provides a benchmark REST API server with clean and buggy modes.
// Use -mode=clean for correct behavior or -mode=buggy for 7 seeded defects.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func main() {
	mode := flag.String("mode", "clean", "server mode: clean or buggy")
	port := flag.Int("port", 0, "listen port (0 = random)")
	flag.Parse()

	mux := newMux(*mode == "buggy")
	addr := fmt.Sprintf(":%d", *port)
	log.Printf("benchmark api server starting on %s (mode=%s)", addr, *mode)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// NewMux creates the HTTP handler. Exported so the test can use it directly.
func NewMux(buggy bool) *http.ServeMux {
	return newMux(buggy)
}

func newMux(buggy bool) *http.ServeMux {
	mux := http.NewServeMux()

	// POST /login
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid json"}`)
			return
		}

		if buggy {
			// API-1: Returns 200 for ANY credentials (broken auth)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			fmt.Fprint(w, `{"token":"fake-token","expires_in":3600}`)
			return
		}

		if body.Username == "admin" && body.Password == "correct-password" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			fmt.Fprint(w, `{"token":"valid-jwt-token","expires_in":3600}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"invalid credentials"}`)
	})

	// GET /users
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		if buggy {
			// API-2: Returns 200 without auth (missing auth check)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			fmt.Fprint(w, `{"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`)
	})

	// GET /users/1
	mux.HandleFunc("/users/1", func(w http.ResponseWriter, r *http.Request) {
		if buggy {
			// API-3: Wrong content type (text/plain instead of application/json)
			w.Header().Set("Content-Type", "text/plain")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"id":1,"name":"Alice","email":"alice@example.com"}`)
	})

	// GET /users/999
	mux.HandleFunc("/users/999", func(w http.ResponseWriter, r *http.Request) {
		if buggy {
			// API-4: Returns 200 with empty user instead of 404
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			fmt.Fprint(w, `{"id":999,"name":"","email":""}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"user not found"}`)
	})

	// POST /users/create
	mux.HandleFunc("/users/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if buggy {
			// API-5: Returns 200 instead of 201
			w.WriteHeader(200)
		} else {
			w.WriteHeader(201)
		}
		fmt.Fprint(w, `{"id":3,"name":"Charlie","created":true}`)
	})

	// DELETE /users/1/delete
	mux.HandleFunc("/users/1/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(405)
			return
		}
		if buggy {
			// API-6: Returns 200 with body instead of 204
			w.WriteHeader(200)
			fmt.Fprint(w, `{"deleted":true}`)
			return
		}
		w.WriteHeader(204)
	})

	// GET /search
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		if buggy {
			// API-7: Returns broken JSON
			w.WriteHeader(200)
			fmt.Fprint(w, `{"query": "test", "results": [BROKEN JSON`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"query":"%s","results":[{"id":1,"match":true}],"total":1}`, q)
	})

	// GET /health (always correct)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"healthy","version":"1.0.0"}`)
	})

	return mux
}
