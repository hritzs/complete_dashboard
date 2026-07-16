package service

import (
	"context"

	"trading-platform/services/contract-master/internal/persistence"
)

type ContractService struct {
	store *persistence.Store
}

func NewContractService(store *persistence.Store) *ContractService {
	return &ContractService{store: store}
}

func (s *ContractService) Load(ctx context.Context, data []persistence.Contract) error {
	return s.store.UpsertContracts(ctx, data)
}

func (s *ContractService) GetLotSize(ctx context.Context, symbol, expiry string) (int, error) {
	return s.store.GetLotSize(ctx, symbol, expiry)
}
