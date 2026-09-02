import csv, sys

csv_path = '/mnt/shared/IndexTokens.csv'
expected = {"NIFTY": 65, "BANKNIFTY": 30, "FINNIFTY": 60, "MIDCPNIFTY": 120, "NIFTYNXT50": 25}

mismatches = []
total_checked = 0
with open(csv_path) as f:
    reader = csv.reader(f)
    header = next(reader)
    for row in reader:
        if len(row) > 10 and row[4] in expected:
            total_checked += 1
            try:
                lot_size = int(row[10]) if row[10] else 0
            except ValueError:
                lot_size = 0
            if lot_size != expected[row[4]]:
                mismatches.append((row[0], row[4], row[6], row[8], row[9], lot_size, expected[row[4]]))

print(f"Checked {total_checked} index F&O rows")
print(f"Found {len(mismatches)} lot-size mismatches vs current NSE values\n")
for m in mismatches[:30]:
    print(f"GreekToken={m[0]:<12} Symbol={m[1]:<10} Expiry={m[2]:<10} Opt={m[3]:<3} Strike={m[4]:<10} CSV_LotSize={m[5]:<5} Expected={m[6]}")

if mismatches:
    print("\n⚠️  CSV is stale. Proceed to Step 2 to refresh it.")
    sys.exit(1)
else:
    print("\n✅ CSV lot sizes match current NSE values.")
