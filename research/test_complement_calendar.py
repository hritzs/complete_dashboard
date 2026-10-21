"""A known special session must not conceal ordinary missing data or executions."""
import unittest
import pandas as pd
from annotate_complement_calendar import CALENDAR, classify


class CalendarTests(unittest.TestCase):
    def setUp(self):
        self.calendar = pd.read_csv(CALENDAR)
        self.row = {'Date': '2021-11-04', 'Eligibility_reason': 'No morning observations',
                    'Opening_rows': 0, 'File_first_source_time': '1900-01-01 18:15:00',
                    'File_last_source_time': '1900-01-01 19:15:01'}

    def test_known_session_is_scheduled_ineligibility(self):
        result = classify(self.row, self.calendar)
        self.assertEqual(result['Eligibility_class'], 'Scheduled_session_outside_policy_window')
        self.assertEqual(result['Raw_eligibility_reason'], 'No morning observations')

    def test_afternoon_2025_is_not_assumed_evening(self):
        self.row.update(Date='2025-10-21', File_first_source_time='1900-01-01 13:45:00',
                        File_last_source_time='1900-01-01 14:45:02')
        self.assertEqual(classify(self.row, self.calendar)['Scheduled_open'], '13:45:00')

    def test_unknown_missing_morning_and_execution_errors_block(self):
        self.row['Date'] = '2021-11-03'
        with self.assertRaises(ValueError): classify(self.row, self.calendar)
        self.row['Date'] = '2021-11-04'
        self.row['Eligibility_reason'] = 'Missing exit quote'
        with self.assertRaises(ValueError): classify(self.row, self.calendar)

    def test_contradictory_quotes_and_unpublished_calendar_block(self):
        self.row['Opening_rows'] = 1
        with self.assertRaises(AssertionError): classify(self.row, self.calendar)
        self.row['Opening_rows'] = 0
        self.calendar.loc[0, 'Published'] = '2021-11-05'
        with self.assertRaises(AssertionError): classify(self.row, self.calendar)

    def test_missing_underlying_remains_a_data_problem(self):
        self.row['Eligibility_reason'] = 'UnderlyingLTP is Arrow null type for entire source file'
        self.assertEqual(classify(self.row, self.calendar)['Eligibility_class'], 'Missing_underlying_data')


if __name__ == '__main__': unittest.main()
