#!/bin/bash -ex
sudo juicefs mount redis://10.10.10.119:6379/1 /mnt/jfs -d
