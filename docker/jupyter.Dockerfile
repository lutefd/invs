# syntax=docker/dockerfile:1.7
FROM python:3.13-slim

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /opt/invs-python
COPY python/pyproject.toml ./
COPY python/research ./research
RUN pip install .
COPY --chmod=0755 docker/jupyter-entrypoint.sh /usr/local/bin/jupyter-entrypoint

WORKDIR /workspace
EXPOSE 8888
