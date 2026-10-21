"""Sample signal coverage without executing options or changing the fixed policy."""
import hashlib,json
from pathlib import Path
import numpy as np
import pandas as pd
import complement_strategy as engine


def main():
    rows=[];original=engine.direction_signal
    samples=['2021-01-04','2024-02-29','2026-10-13']
    for date in samples:
        def observe(underlying,decision,high,low):
            direction,er=original(underlying,decision,high,low)
            p=decision-engine.GRID[0];current=underlying[p-1]
            rows.append({'Date':date,'Decision':engine.clock(decision),'Efficiency_1second_path':er,
                'Opening_high':high,'Opening_low':low,'Known_underlying':current,'Original_direction':direction,
                'Beyond_opening_buffer':bool(current>high+.1*(high-low) or current<low-.1*(high-low))})
            # Data audit only: prevent every option-price read and PnL calculation.
            return 0,er
        engine.direction_signal=observe
        result=engine.simulate_day(date);assert not result['Error'],result['Error']
        print(date,'observed minute decisions',sum(row['Date']==date for row in rows),flush=True)
    engine.direction_signal=original
    frame=pd.DataFrame(rows);frame.to_csv(engine.OUT/'sample_signal_coverage.csv',index=False)
    groups=[]
    for date,g in frame.groupby('Date'):
        groups.append({'Date':date,'Decisions':len(g),'Beyond_buffer':int(g.Beyond_opening_buffer.sum()),
            'ER_at_least_030':int(g.Efficiency_1second_path.ge(.3).sum()),'Original_signals':int(g.Original_direction.ne(0).sum()),
            'ER_median':float(g.Efficiency_1second_path.median()),'ER_p90':float(g.Efficiency_1second_path.quantile(.9)),
            'ER_max':float(g.Efficiency_1second_path.max())})
    grouped=pd.DataFrame(groups)
    with pd.ExcelWriter(engine.OUT/'sample_signal_coverage.xlsx',engine='openpyxl') as writer:
        grouped.to_excel(writer,sheet_name='Coverage',index=False);frame.to_excel(writer,sheet_name='Past_only_decisions',index=False)
    source={'Audit_code_SHA256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        'Fixed_engine_SHA256':hashlib.sha256(Path(engine.__file__).read_bytes()).hexdigest(),
        'Samples':samples,'Strategy_PnL_calculated':False,'Parameters_changed':False}
    (engine.OUT/'sample_signal_audit.json').write_text(json.dumps(source,indent=2)+'\n')
    print(grouped.to_string(index=False),flush=True)


if __name__=='__main__':main()
