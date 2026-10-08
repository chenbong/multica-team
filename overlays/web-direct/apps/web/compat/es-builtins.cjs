// Run before React hydration and route modules. CSS feature support is not a
// reliable test for JavaScript APIs; core-js detects each method independently.
require('./node_modules/core-js/actual/structured-clone.js');
require('./node_modules/core-js/actual/array/to-sorted.js');
require('./node_modules/core-js/actual/array/find-last.js');
require('./node_modules/core-js/actual/array/find-last-index.js');
