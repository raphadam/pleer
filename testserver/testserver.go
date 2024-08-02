package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("RECEIVED REQUEST FROM CLIENT")

		fmt.Fprintf(w, "hello from the testing server!")
	})

	fmt.Println("server is listening on port 8080...")

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal("server closed", err)
	}
}
