#!/usr/bin/bash

docker build -f Dockerfile.server-base -t meqa/python .
docker build -f Dockerfile.server -t yingxie3/mqserver .
docker build -f Dockerfile.client -t meqa/go .
