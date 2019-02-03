#!/bin/sh

cd config/default && kustomize build | sudo kubectl --validate=false apply -f -
