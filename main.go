package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"
)

var (
	suggestions = NewSuggestionsMap()
)

func main() {
	fname := flag.String("file", "suggestions.json", "file with suggestions data")
	periodSec := flag.Int("period", 15, "updating period")
	port := flag.Int("port", 8080, "listening port")
	timeoutSec := flag.Int("timeout", 2, "request timeout")
	flag.Parse()

	go func() {
		for {
			suggestions.Load(*fname)
			<-time.After(time.Duration(*periodSec) * time.Minute)
		}
	}()

	router := Router{http.NewServeMux()}
	router.Post("/v1/api/suggest", withTimeout(Suggest, time.Duration(*timeoutSec)*time.Second))

	fmt.Printf("Server listening on 0.0.0.0:%d\n", *port)
	http.ListenAndServe(fmt.Sprintf(":%d", *port), router)
}

// handler

func Suggest(w http.ResponseWriter, r *http.Request) {
	obj := new(SuggestionRequest)

	if err := bind(r.Body, obj); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := obj.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	body, err := json.Marshal(suggestions.ListByKey(*obj.Input))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeSuccess(w, http.StatusOK, body)
}

// router

type Router struct {
	*http.ServeMux
}

func (r *Router) Post(url string, handler http.HandlerFunc) {
	post := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}

		handler.ServeHTTP(w, r)
	})

	r.Handle(url, post)
}

// storage

type SuggestionsMap struct {
	mx   sync.Mutex
	data map[string][]mapItem
}

type mapItem struct {
	Cost int
	Name string
}

func NewSuggestionsMap() SuggestionsMap {
	return SuggestionsMap{
		data: make(map[string][]mapItem),
	}
}

func (s *SuggestionsMap) Load(path string) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		log.Println(err)
		return
	}

	suggestions := make([]suggestionDTO, 0)
	if err = json.Unmarshal(data, &suggestions); err != nil {
		log.Println(err)
		return
	}

	s.init(suggestions)
}

func (s *SuggestionsMap) ListByKey(key string) []Suggestion {
	s.mx.Lock()
	items, ok := s.data[key]
	s.mx.Unlock()
	if !ok {
		return []Suggestion{}
	}

	suggestions := make([]Suggestion, 0, len(items))
	for i := range items {
		suggestions = append(suggestions, Suggestion{
			Position: i,
			Text:     items[i].Name,
		})
	}

	return suggestions
}

func (s *SuggestionsMap) init(dtos []suggestionDTO) {
	data := make(map[string][]mapItem)
	for _, dto := range dtos {
		item := mapItem{
			Cost: dto.Cost,
			Name: dto.Name,
		}

		if _, ok := data[dto.ID]; !ok {
			data[dto.ID] = make([]mapItem, 1)
			data[dto.ID][0] = item
			continue
		}

		data[dto.ID] = append(data[dto.ID], item)

		for i := len(data[dto.ID]) - 1; i > 0; i-- {
			if data[dto.ID][i].Cost < data[dto.ID][i-1].Cost {
				data[dto.ID][i], data[dto.ID][i-1] = data[dto.ID][i-1], data[dto.ID][i]
			}
		}
	}

	s.mx.Lock()
	s.data = data
	s.mx.Unlock()
}

// models

type SuggestionRequest struct {
	Input *string `json:"input"`
}

func (s *SuggestionRequest) Validate() error {
	if s.Input == nil {
		return fmt.Errorf("input is empty")
	}

	return nil
}

type SuggestionsResponse struct {
	Suggestions []Suggestion `json:"suggestions"`
}

type Suggestion struct {
	Text     string `json:"text"`
	Position int    `json:"position"`
}

type suggestionDTO struct {
	ID   string `json:"id"`
	Cost int    `json:"cost"`
	Name string `json:"name"`
}

// utils

func bind(body io.ReadCloser, obj interface{}) error {
	defer body.Close()

	data, err := ioutil.ReadAll(body)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, obj)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err = w.Write([]byte(fmt.Sprintf(`{"error": "%v"}`, err))); err != nil {
		log.Println(err)
	}
}

func writeSuccess(w http.ResponseWriter, status int, body []byte) {
	if body == nil {
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Println(err)
	}
}

func withTimeout(f http.HandlerFunc, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		done := make(chan struct{})
		go func() {
			f.ServeHTTP(w, r)
			done <- struct{}{}
		}()

		select {
		case <-time.After(timeout):
			writeError(w, http.StatusInternalServerError, fmt.Errorf("timeout"))
		case <-done:
		}
	}
}
