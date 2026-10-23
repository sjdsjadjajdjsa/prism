[ "$(python3 report.py)" = "$(cat expected.txt)" ] && [ $(grep -c 'upper()' report.py) -eq 1 ]
