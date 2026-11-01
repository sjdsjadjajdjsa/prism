#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
w=1000
h=600
fps=10
graph="
[0:v]trim=5:32.6,setpts=PTS-STARTPTS,format=rgb24,drawbox=x=1760:y=1010:w=160:h=60:color=0x171717:t=fill,crop=1800:1080:60:0,drawtext=font='JetBrains Mono':text='4x speed':fontsize=26:fontcolor=0x8a8798:x=w-tw-36:y=1028:enable='between(t,10,22)',scale=$w:$h:flags=lanczos,fps=$fps,format=yuv444p[term];
[0:v]trim=32.6:41.0,setpts=PTS-STARTPTS,crop=1200:720:360:200,scale=$w:$h:flags=lanczos,fps=$fps,format=yuv444p,tpad=stop_mode=clone:stop_duration=2.5[app];
[term][app]xfade=transition=fade:duration=0.5:offset=27.1,fade=t=out:st=37.4:d=0.6,mpdecimate=hi=512:lo=256:frac=0.5,split[a][b];
[a]palettegen=max_colors=256:stats_mode=full[p];
[b][p]paletteuse=dither=none:diff_mode=rectangle"
ffmpeg -v error -y -i prism-demo.mp4 -filter_complex "$graph" -fps_mode vfr -loop 0 prism-demo.gif
ls -la prism-demo.gif
