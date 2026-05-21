#!/usr/bin/env node

const readline = require('readline');

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout
});

function fullEncode(str) {
  return encodeURIComponent(str).replace(/[!'()*]/g, (c) => {
    return '%' + c.charCodeAt(0).toString(16).toUpperCase();
  });
}

rl.question('Enter text to encode: ', (answer) => {
  console.log('Original:', answer);
  console.log('Encoded:', fullEncode(answer));
  rl.close();
});

