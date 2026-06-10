#!/bin/bash

# XTS Interactive API Credentials (from cred.py)
export XTS_API_KEY="6debd1fc7b75c6d2291950"
export XTS_API_SECRET="Mrbp544@RD"
export XTS_SOURCE="WEBAPI"
# For PRO/Dealer accounts, ClientID is strictly required! Set this to your target Client ID.
export XTS_CLIENT_ID="TEST49"

echo "Loading credentials and starting Execution Gateway..."

# Run the Go application
go run main.go