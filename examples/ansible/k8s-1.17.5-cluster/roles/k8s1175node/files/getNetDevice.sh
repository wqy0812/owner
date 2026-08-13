#!/bin/bash

VFs=1000

for ((i=1; i<=$VFs; i++))
do
  tmp="${tmp}cbond${i};" 
done

echo -ne ${tmp}
