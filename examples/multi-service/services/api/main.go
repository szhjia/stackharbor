package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("DEMO_PORT")
	if port == "" {
		port = "18080"
	}
	name := os.Getenv("DEMO_NAME")
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "%s is running\n", name) })
	go func() {
		for {
			fmt.Println(name, "heartbeat", time.Now().Format(time.RFC3339))
			time.Sleep(2 * time.Second)
		}
	}()
	fmt.Println(name, "listening", port)
	if e := http.ListenAndServe("127.0.0.1:"+port, nil); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
