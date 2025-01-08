package codecs

import (
	"log"
	"testing"
	"time"
)

func TestMarshal(t *testing.T) {
	type Data struct {
		ID   string    `json:"id"`
		Time time.Time `json:"time"`
	}

	data := &Data{
		ID:   "1",
		Time: time.Now(),
	}
	b, _ := Marshal(data)
	log.Println(string(b))
}
