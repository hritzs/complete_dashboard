"""Annotate already-audited admission failures; never alter replay checkpoints.

The calendar is a verified five-session reference, not a complete holiday calendar.
The original audit and raw failures remain immutable evidence.
"""
import hashlib
import json
import pickle
import shutil
from pathlib import Path

import pandas as pd

ROOT = Path(__file__).resolve().parent
OUT = ROOT / 'output/complement_strategy'
CALENDAR = ROOT / 'muhurat_sessions.csv'


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def classify(row, calendar):
    row = dict(row)
    reason = row['Eligibility_reason']
    row.update(Raw_eligibility_reason=reason, Session_type='Not_classified_by_this_reference',
               Eligibility_class='', Timezone='Asia/Kolkata', Scheduled_open='',
               Scheduled_close='', Circular='', Published='', Source_URL='')
    dates = calendar.set_index('Date')
    if reason == 'No morning observations':
        if row['Date'] not in dates.index:
            raise ValueError('Unexplained missing morning: calendar investigation required')
        session = dates.loc[row['Date']].to_dict()
        assert session['Session_type'] == 'Diwali_Muhurat'
        assert session['Timezone'] == 'Asia/Kolkata'
        assert session['Published'] < row['Date'], 'Calendar must be known before the session'
        assert '10:30:00' < session['Scheduled_open'] < session['Scheduled_close']
        assert row['Opening_rows'] == 0
        first = str(row['File_first_source_time']).split()[-1]
        last = str(row['File_last_source_time']).split()[-1]
        # Post-close source records are retained, never reinterpreted as executable quotes.
        assert session['Scheduled_open'] <= first <= session['Scheduled_close'] <= last
        row.update(session, Eligibility_class='Scheduled_session_outside_policy_window',
                   Eligibility_reason='Diwali/Muhurat scheduled session outside fixed morning strategy window')
    elif reason == 'UnderlyingLTP is Arrow null type for entire source file':
        row['Eligibility_class'] = 'Missing_underlying_data'
    elif reason == 'Fewer than600 valid opening underlying seconds':
        row['Eligibility_class'] = 'Insufficient_opening_data'
    else:
        raise ValueError('Unexpected admission reason: ' + reason)
    return row


def verify():
    manifest = json.loads((OUT / 'calendar_annotation_manifest.json').read_text())
    for name, digest in manifest['Inputs'].items():
        assert sha(ROOT / name) == digest, name
    for name, digest in manifest['Outputs'].items():
        assert sha(OUT / name) == digest, name
    original = pd.read_csv(OUT / 'calendar_provenance/eligibility_audit_original.csv',
                           dtype={'Tick_mtime_ns': str})
    calendar = pd.read_csv(CALENDAR)
    expected = pd.DataFrame([classify(row, calendar) for row in original.to_dict('records')])
    actual = pd.read_csv(OUT / 'eligibility_audit.csv', dtype={'Tick_mtime_ns': str}).fillna('')
    pd.testing.assert_frame_equal(expected.fillna(''), actual, check_dtype=False)
    assert actual.Eligibility_class.value_counts().to_dict() == {
        'Scheduled_session_outside_policy_window': 5, 'Missing_underlying_data': 5,
        'Insufficient_opening_data': 1}
    run = json.loads((OUT / 'run_manifest.json').read_text())
    assert set(actual.Date) == set(run['Raw_failed_dates'])
    for row in actual.to_dict('records'):
        date = row['Date']
        rec = pickle.loads((OUT / 'days' / (date + '.pkl')).read_bytes())
        raw = OUT / 'raw_ineligible_days' / (date + '.pkl')
        assert rec['Raw_exception_SHA256'] == sha(raw)
        assert rec['Eligibility_resolution'] == row['Raw_eligibility_reason']
        assert not rec['Error'] and all(not r['Traded'] and r['Net_PnL'] == 0 for r in rec['Rows'])
        assert row['Tick_size'] == rec['Tick_size'] and row['Tick_mtime_ns'] == rec['Tick_mtime_ns']
    return manifest


def main():
    archive = OUT / 'calendar_provenance'
    archive.mkdir(exist_ok=True)
    source = archive / 'eligibility_audit_original.csv'
    if not source.exists():
        for suffix in ('csv', 'xlsx'):
            shutil.copy2(OUT / ('eligibility_audit.' + suffix), archive / ('eligibility_audit_original.' + suffix))
    original = pd.read_csv(source, dtype={'Tick_mtime_ns': str})
    calendar = pd.read_csv(CALENDAR)
    assert not calendar.Date.duplicated().any()
    rows = pd.DataFrame([classify(row, calendar) for row in original.to_dict('records')])
    rows.to_csv(OUT / 'eligibility_audit.csv', index=False)
    with pd.ExcelWriter(OUT / 'eligibility_audit.xlsx') as writer:
        rows.to_excel(writer, sheet_name='Eligibility_audit', index=False)
        calendar.to_excel(writer, sheet_name='Muhurat_calendar', index=False)
        original.to_excel(writer, sheet_name='Original_audit', index=False)
    inputs = [Path(__file__), CALENDAR, source, archive / 'eligibility_audit_original.xlsx',
              OUT / 'run_manifest.json', OUT / 'raw_run_manifest.json',
              *sorted((OUT / 'days').glob('*.pkl')), *sorted((OUT / 'raw_ineligible_days').glob('*.pkl'))]
    manifest = {'Status': 'PASS', 'Verified_on': '2026-10-21',
                'Scope': 'Calendar annotation only; all 1430 original replay checkpoints preserved',
                'Inputs': {str(p.relative_to(ROOT)): sha(p) for p in inputs},
                'Outputs': {p.name: sha(p) for p in (OUT / 'eligibility_audit.csv', OUT / 'eligibility_audit.xlsx')}}
    (OUT / 'calendar_annotation_manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    verify()
    print(rows[['Date', 'Eligibility_class', 'Scheduled_open', 'Scheduled_close']].to_string(index=False))


if __name__ == '__main__':
    main()
