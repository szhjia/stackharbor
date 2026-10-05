package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ready\n")) })
	mux.HandleFunc("/menu", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s /menu — served 3 drinks", r.Method)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]string{
			{"name": "Harbor espresso", "price": "$3.00"},
			{"name": "Oat latte", "price": "$4.50"},
			{"name": "Iced matcha", "price": "$5.00"},
		})
	})
	log.Print("Harbor Cafe menu API listening on http://127.0.0.1:18281")
	log.Fatal(http.ListenAndServe("127.0.0.1:18281", mux))
}
