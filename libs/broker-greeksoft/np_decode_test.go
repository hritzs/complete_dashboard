package greeksoft

import (
	"encoding/json"
	"testing"
)

func TestNetPositionDecode_NumbersOrStrings(t *testing.T) {
	raw := `{"response":{"data":{"islast":2,"noofrecords":"1","stockDetails":[{"netQty":-65,"DayNetAmt":-2681.25,"token":102040710,"description":"NIFTY 06OCT26 CE 22700","ProductType":1}]}}}`
	var r NetPositionResponse
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	p := r.Response.Data.StockDetails[0]
	if p.NetQty != "-65" || p.Token != "102040710" || p.Description != "NIFTY 06OCT26 CE 22700" || r.Response.Data.IsLast != "2" {
		t.Fatalf("%+v", p)
	}
}
