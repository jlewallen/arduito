#!/bin/bash

rm -f package_adafruit_index.json
rm -f package_index.json
wget https://raw.githubusercontent.com/adafruit/arduino-board-index/gh-pages/package_adafruit_index.json
wget http://downloads.arduino.cc/packages/package_index.json
