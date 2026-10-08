package trading

import (
	"context"
	"fmt"
)

type BuildMode string

const (
	BuildModeManual    BuildMode = "MANUAL"
	BuildModeConfig    BuildMode = "CONFIG"
	BuildModeAutomated BuildMode = "AUTOMATED"
	BuildModeLUT       BuildMode = "LUT" // fired by the LUT's first YES (lut_build.go)
)

type FinalBuildRequest struct {
	Mode             BuildMode
	UserID           string
	BrokerName       string
	AccountID        string
	ExchangeSegment  string
	ProductType      string
	Symbol           string
	TargetExpiry     string
	Lots             int
	OrderLotsPerCall int
	DeltaNeutral     bool
	Risk             *BuildRiskConfig
	// Optional exact strikes (both or neither): the LUT build sells the
	// strike the LUT evaluated, not whatever is ATM a moment later.
	CEStrikePrice int
	PEStrikePrice int
}

func (s *Service) ExecuteFinalBuild(
	ctx context.Context,
	req FinalBuildRequest,
) (*DeployStraddleResponse, error) {
	if req.Lots <= 0 {
		return nil, fmt.Errorf("final build lots must be greater than zero")
	}

	if req.Symbol == "" {
		return nil, fmt.Errorf("final build symbol is required")
	}

	if req.BrokerName == "" {
		return nil, fmt.Errorf("final build broker_name is required")
	}

	if req.AccountID == "" {
		return nil, fmt.Errorf("final build account_id is required")
	}

	return s.DeployStraddle(ctx, DeployStraddleRequest{
		UserID:          req.UserID,
		BrokerName:      req.BrokerName,
		AccountID:       req.AccountID,
		ExchangeSegment: req.ExchangeSegment,
		// Empty lets DeployStraddle apply its per-broker default (NRML for
		// GreekSoft) -- the same one a manual build gets. This used to force
		// MIS, which GreekSoft maps to intraday product 0.
		ProductType:      req.ProductType,
		Symbol:           req.Symbol,
		Lots:             req.Lots,
		TargetExpiry:     req.TargetExpiry,
		OrderLotsPerCall: req.OrderLotsPerCall,
		DeltaNeutral:     req.DeltaNeutral,
		Risk:             req.Risk,
		CEStrikePrice:    req.CEStrikePrice,
		PEStrikePrice:    req.PEStrikePrice,
	})
}
