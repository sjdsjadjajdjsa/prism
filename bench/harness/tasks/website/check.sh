grep -qi localStorage index.html && python3 -c "import html.parser;html.parser.HTMLParser().feed(open('index.html').read())"
