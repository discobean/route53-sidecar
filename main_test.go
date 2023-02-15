package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func Test_getEcsMetadata(t *testing.T) {
	const want = "127.0.0.1"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Name":"curl","Networks":[{"IPv4Addresses":["` + want + `"]}]}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	os.Setenv("ECS_CONTAINER_METADATA_URI_V4", server.URL)

	got, err := getEcsMetadata()
	if err != nil {
		t.Errorf("getEcsMetadata() error = %v", err)
		return
	}
	if got.Networks[0].IPv4Addresses[0] != want {
		t.Errorf("getEcsMetadata() = %v, want %v", got, want)
	}
}
