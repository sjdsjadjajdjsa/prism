# test.js must be untouched and must pass
echo "7b2b9cd802d00e3b3ad2e9675a7d751f7e196ac371c88c9403aa3c236d430391  test.js" | sha256sum -c --quiet - || exit 1
timeout 20 node test.js | tail -1 | grep -qx 'ALL PASSED'
