#!/bin/sh

cd config && kustomize build | sudo kubectl --validate=false apply -f -
