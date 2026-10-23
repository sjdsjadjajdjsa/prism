# Third-party harnesses for the comparison, layered on Prism's own agent image so
# every harness sees the same OS, python3, node, git and ripgrep.
# build: docker build --network host -f bench/harness/Dockerfile -t prism-harness:20261022 bench/harness
ARG PRISM_IMAGE=prism-agent:latest
FROM ${PRISM_IMAGE}
USER root
ENV HOME=/tmp/harness-home PIP_DISABLE_PIP_VERSION_CHECK=1
RUN python3 -m venv /opt/aider \
 && /opt/aider/bin/pip install --no-cache-dir aider-chat==0.86.2 \
 && python3 -m venv /opt/mini \
 && /opt/mini/bin/pip install --no-cache-dir mini-swe-agent==2.4.6 \
 && npm install -g opencode-ai@1.19.4
# mini-swe-agent 2.4.6 pulls litellm 1.106.2, which imports the undeclared `mcp`
# package on every tool-calling completion (ModuleNotFoundError) -> add it.
RUN /opt/mini/bin/pip install --no-cache-dir mcp==2.3.0
WORKDIR /workspace
ENTRYPOINT ["python3", "/harness/entry.py"]
