package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"

	xts "trading-platform/libs/broker-xts"
	"trading-platform/libs/broker-xts/models"
)

func EnsureLogin(c *xts.Client) error {
	c.Mu.Lock()
	defer c.Mu.Unlock()

	if c.Token != "" {
		return nil
	}

	if c.AppKey == "" || c.SecretKey == "" {
		return fmt.Errorf("missing XTS API credentials")
	}

	reqBody := models.LoginRequest{AppKey: c.AppKey, SecretKey: c.SecretKey, Source: "WEBAPI"}
	lb, _ := json.Marshal(reqBody)

	lresp, err := c.HTTP.Post(c.BaseURL+"/interactive/user/session", "application/json", bytes.NewBuffer(lb))
	if err != nil {
		return err
	}
	defer lresp.Body.Close()

	var lr models.LoginResponse
	if err := json.NewDecoder(lresp.Body).Decode(&lr); err == nil && lr.Type == "success" && lr.Result.Token != "" {
		c.Token = lr.Result.Token
		log.Println("✅ XTS Interactive Login successful.")
		return nil
	}

	return fmt.Errorf("XTS Login failed with status %d", lresp.StatusCode)
}
