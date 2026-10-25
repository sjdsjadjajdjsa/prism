# Benchmarks

How fast Prism is, and how it compares with other coding agents. Back to the [README](../README.md).


## How it stays fast

The router picks the model for every step:

- **Races free models.** If the first one hasn't started answering in a few seconds, the next joins the race, and the first to answer wins.
- **Learns which models are quick** and remembers it between runs.
- **Skips models that are busy or down**, and tries them again later.
- **Fast lane:** on NVIDIA's Nemotron models, short questions skip the slow thinking step.
- **Gets out of loops.** A model that keeps writing the same lines is stopped early, its text is taken back, and another model answers. A model that keeps making the same call with the same result is told so and the next step goes to another model; after two switches Prism stops and says why, instead of using up the free models.

## Benchmarks

Each row is one run of the same tasks on the same computer. A task only counts if a script confirms the result works.

<p align="center"><img src="bench-coding.png" alt="Total time for five coding tasks across versions of Prism" width="820"></p>
<p align="center"><img src="bench-chat.png" alt="Simple questions: 10.7s to 0.4s" width="820"></p>
<p align="center"><img src="bench-website.png" alt="One website task: 1171s to 24s" width="820"></p>
<p align="center"><img src="bench-light.png" alt="prism starts in 0.27s instead of 11s, idles at 0.2% CPU and 0 KB/s, and uses 9.1 MB per extra session instead of 10.9" width="820"></p>

## Against other coding agents

Same five tasks, same free models, only the agent changes. Every agent went through the same router, three runs per task (60 runs in all).

<p align="center"><img src="bench-harness.png" alt="Four coding agents on five tasks: median seconds and passes for each" width="820"></p>

Prism, opencode and mini-swe-agent passed all 15 runs and Aider passed 14. Times differ by task: opencode and Aider were quicker on most tasks, because Prism also runs and checks its own work. Method, versions and every run: [bench/harness](../bench/harness/README.md).

With one model pinned (no router), Prism and Aider:

<p align="center"><img src="bench-pinned.png" alt="Prism and Aider on one pinned model: seconds and passes for each task" width="820"></p>

Free models are shared, so the same run can be 30% faster or slower on another day. Compare rows loosely. The raw numbers are in [bench/history.json](../bench/history.json), `python3 bench/charts.py` rebuilds the charts (it needs Chromium for the PNGs), and `bench/run.sh` and `bench/chat.sh` measure your own setup.
