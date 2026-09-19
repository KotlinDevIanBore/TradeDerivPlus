package models_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type vector struct {
	Direction string `json:"direction"`
	Entry     string `json:"entry"`
	Current   string `json:"current"`
	Stake     string `json:"stake"`
	Want      string `json:"want"`
}

// TestDumpPnLVectors writes the Go implementation's answers to a file the
// frontend parity check reads, so one source of truth drives both languages.
//
//	go test ./internal/models -run TestDumpPnLVectors -dump
func TestDumpPnLVectors(t *testing.T) {
	inputs := []struct{ dir, entry, current, stake string }{
		{models.DirectionBuy, "100", "110", "50"},
		{models.DirectionBuy, "100", "90", "50"},
		{models.DirectionSell, "100", "90", "50"},
		{models.DirectionSell, "100", "110", "50"},
		{models.DirectionBuy, "100", "100", "50"},
		{models.DirectionBuy, "1.08540", "1.08640", "1000"},
		{models.DirectionSell, "1.08540", "1.08440", "1000"},
		{models.DirectionBuy, "4685.559", "4690.123", "250"},
		{models.DirectionSell, "9977.7528", "9950.0001", "75.50"},
		{models.DirectionBuy, "157.220", "157.905", "33.33"},
		{models.DirectionBuy, "0.00001", "0.00002", "10"},
		{models.DirectionSell, "12345.6789", "12345.6788", "1000000"},
	}

	out := make([]vector, 0, len(inputs))
	for _, in := range inputs {
		got := models.PnL(in.dir,
			decimal.RequireFromString(in.entry),
			decimal.RequireFromString(in.current),
			decimal.RequireFromString(in.stake))
		out = append(out, vector{in.dir, in.entry, in.current, in.stake, got.String()})
	}

	blob, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("../../scripts/pnl_vectors.json", blob, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d vectors", len(out))
}
