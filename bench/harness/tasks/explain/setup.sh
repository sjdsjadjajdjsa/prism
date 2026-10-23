for i in 1 2 3; do for j in $(seq 1 $((i*3))); do echo "def f${i}_${j}(): pass" >> m$i.py; done; done
