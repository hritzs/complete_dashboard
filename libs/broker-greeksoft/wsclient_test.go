package greeksoft

import "testing"

func TestBuildIrisWebSocketURL(t *testing.T) {
	got, err := buildGreekSoftWebSocketURL("192.168.1.10", "3031")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "ws://192.168.1.10:3031"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestClassifyIrisFrame(t *testing.T) {
	raw := []byte(`{"response":{"streaming_type":"OrderResponse","svcName":"OrderResponse","data":{"gorderid":"12345","order_status":"Pending"}}}`)
	frame := classifyIrisFrame(raw)

	if frame.StreamingType != "OrderResponse" {
		t.Fatalf("streaming type got %q", frame.StreamingType)
	}
	if frame.ServiceName != "OrderResponse" {
		t.Fatalf("service name got %q", frame.ServiceName)
	}
}

func TestClassifyUnknownIrisFrame(t *testing.T) {
	frame := classifyIrisFrame([]byte("not-json"))
	if frame.StreamingType != "UNKNOWN" {
		t.Fatalf("streaming type got %q", frame.StreamingType)
	}
}
