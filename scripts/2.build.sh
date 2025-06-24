#!/bin/bash

cd frostbeat

KO_DOCKER_REPO=kind.local ko build --base-import-paths .

cd ../auger

KO_DOCKER_REPO=kind.local ko build --base-import-paths .

cd ..