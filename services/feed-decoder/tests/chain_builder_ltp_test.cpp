// g++ -std=c++17 -Iinclude tests/chain_builder_ltp_test.cpp src/chain_builder.cpp src/greeks_calculator.cpp -o /tmp/cbl && /tmp/cbl
// A tick with LTP 0 must not wipe a strike's last real price (BSE sends ~30%
// of SENSEX updates with LTP 0; 2026-11-05 they kept zeroing the chain).
#include "decoder/chain_builder.hpp"
#include <cassert>
#include <cstdio>

using namespace decoder;

int main() {
    ChainBuilder cb;
    OptionChain c;
    c.symbol = "SENSEX";
    c.expiry = "05-NOV-2026";
    c.dte = 0.5 / 365.0;
    OptionStrike s;
    s.strike = 79200;
    s.ce_token = 1134412;
    s.pe_token = 1134486;
    c.strikes.push_back(s);
    cb.set_chain("SENSEX_05-NOV-2026", c);

    cb.update_option_price(1134412, 167.80, 167.5, 168.0, 0, 0);
    cb.update_option_price(1134486, 170.85, 170.5, 171.0, 0, 0);
    cb.update_option_price(1134412, 0.0, 0.0, 0.0, 0, 0);   // BSE update with no trade price
    cb.update_option_price(1134486, 0.0, 170.6, 0.0, 0, 0); // ... bid only

    OptionChain out;
    assert(cb.get_chain("SENSEX_05-NOV-2026", out));
    assert(out.strikes.size() == 1);
    assert(out.strikes[0].ce_ltp == 167.80);   // kept
    assert(out.strikes[0].pe_ltp == 170.85);   // kept
    assert(out.strikes[0].pe_bid == 170.6);    // a real bid still updates
    assert(out.strikes[0].ce_ask == 168.0);

    cb.update_option_price(1134412, 169.10, 0.0, 0.0, 0, 0); // a real trade price still updates
    assert(cb.get_chain("SENSEX_05-NOV-2026", out));
    assert(out.strikes[0].ce_ltp == 169.10);
    printf("OK: chain_builder_ltp_test passed\n");
}
