package uri

import (
	"net/url"
	"reflect"
	"testing"
	"time"
)

type data struct {
	Scheme    string
	Username  string
	Password  string
	Namespace string
	Test      string
	Child     struct {
		Test     string
		TestName string
		Test2    int
	}
	Host     []string
	Test2    int
	Test3    float64
	Duration time.Duration
}

func TestUnmarshal(t *testing.T) {
	// Construct URL directly because Go 1.26+ rejects commas in the host portion of url.Parse.
	uriValue := &url.URL{
		Scheme:   "https",
		User:     url.UserPassword("user", "pass"),
		Host:     "host1:80,host2.example.com:443",
		Path:     "/namespace",
		RawQuery: "test=new&test2=3232&test3=3.1415&child.TestName=go&child.test=323&duration=10s",
	}
	var testData data
	err := Unmarshal(uriValue, &testData)
	if err != nil {
		t.Fatal(err)
		return
	}
	verifyData := data{
		Scheme:   "https",
		Username: "user",
		Password: "pass",
		Host: []string{
			"host1:80",
			"host2.example.com:443",
		},
		Namespace: "namespace",
		Test:      "new",
		Test2:     3232,
		Test3:     3.1415,
		Duration:  10 * time.Second,
		Child: struct {
			Test     string
			TestName string
			Test2    int
		}{Test: "323", TestName: "go"},
	}
	if !reflect.DeepEqual(testData, verifyData) {
		t.Log("data does not match")
		t.Failed()
	}
}
