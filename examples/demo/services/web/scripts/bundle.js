'use strict';

// Joins the modules of src/ into dist/bundle.js, in name order.
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const srcDir = path.join(root, 'src');
const files = fs.readdirSync(srcDir).filter((f) => f.endsWith('.js')).sort();
if (files.length === 0) {
  throw new Error('no modules in src/');
}
const parts = files.map((f) => `// ${f}\n${fs.readFileSync(path.join(srcDir, f), 'utf8')}`);
fs.mkdirSync(path.join(root, 'dist'), { recursive: true });
fs.writeFileSync(path.join(root, 'dist', 'bundle.js'), parts.join('\n'));
console.log(`bundled ${files.length} module(s) into dist/bundle.js`);
