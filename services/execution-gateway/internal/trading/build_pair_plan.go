package trading

import "fmt"

type BuildPairRound struct {
	Index        int
	CELots       int
	PELots       int
	ExcessSide   string
	HasExcessLot bool
}

func PlanBuildPairRounds(ceLots, peLots int) ([]BuildPairRound, error) {
	if ceLots <= 0 || peLots <= 0 {
		return nil, fmt.Errorf(
			"safe paired build requires both legs: ce=%d pe=%d",
			ceLots,
			peLots,
		)
	}

	paired := ceLots
	if peLots < paired {
		paired = peLots
	}

	excessSide := ""
	excessLots := 0

	if ceLots > peLots {
		excessSide = "CE"
		excessLots = ceLots - peLots
	} else if peLots > ceLots {
		excessSide = "PE"
		excessLots = peLots - ceLots
	}

	rounds := make([]BuildPairRound, 0, paired)
	accumulator := 0

	for index := 1; index <= paired; index++ {
		round := BuildPairRound{
			Index:  index,
			CELots: 1,
			PELots: 1,
		}

		if excessLots > 0 {
			accumulator += excessLots

			if accumulator >= paired {
				accumulator -= paired
				round.ExcessSide = excessSide
				round.HasExcessLot = true
			}
		}

		rounds = append(rounds, round)
	}

	return rounds, nil
}
