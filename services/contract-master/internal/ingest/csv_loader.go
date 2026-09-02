package ingest

import (
	"encoding/csv"
	"os"
	"strconv"
	"time"

	"trading-platform/services/contract-master/internal/persistence"
)

func LoadContracts(path string) ([]persistence.Contract, error) {

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var contracts []persistence.Contract

	for i, row := range rows {

		// skip header
		if i == 0 {
			continue
		}

		// safety check
		if len(row) < 6 {
			continue
		}

		token, _ := strconv.ParseInt(row[0], 10, 64)
		symbol := row[1]

		// ✅ FIX: parse string → time.Time
		expiry, err := time.Parse("2006-01-02", row[2])
		if err != nil {
			continue // skip bad row
		}

		strike, _ := strconv.ParseFloat(row[3], 64)
		optionType := row[4]
		lotSize, _ := strconv.Atoi(row[5])

		if token == 0 || lotSize == 0 {
			continue
		}

		contracts = append(contracts, persistence.Contract{
			BrokerToken:    token,
			Exchange:       "NSE",
			Symbol:         symbol,
			InstrumentType: "OPT",
			ExpiryDate:     expiry, // ✅ now correct type
			StrikePrice:    strike,
			OptionType:     optionType,
			LotSize:        lotSize,
			TickSize:       0.05,
			RawDetails:     nil,
		})
	}

	return contracts, nil
}
