package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"time"
)

var menuPage = template.Must(template.New("menu").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Harbor Cafe</title><h1>Harbor Cafe</h1><p>A fictional, local demo. Menu data comes from the Menu API.</p><ul>{{range .}}<li>{{.name}} — {{.price}}</li>{{end}}</ul></html>`))

func main() {
	client := &http.Client{Timeout: 2 * time.Second}
	loadMenu := func() ([]map[string]string, error) {
		resp, err := client.Get("http://127.0.0.1:18281/menu")
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var menu []map[string]string
		err = json.NewDecoder(resp.Body).Decode(&menu)
		return menu, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := loadMenu(); err != nil {
			http.Error(w, "Menu API unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		menu, err := loadMenu()
		if err != nil {
			http.Error(w, "Menu API unavailable", http.StatusServiceUnavailable)
			return
		}
		log.Printf("%s / — loaded %d drinks from Menu API", r.Method, len(menu))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		menuPage.Execute(w, menu)
	})
	log.Print("Harbor Cafe order counter listening on http://127.0.0.1:18282")
	log.Print("Dependency: Menu API must be ready before this service starts")
	log.Fatal(http.ListenAndServe("127.0.0.1:18282", mux))
}
