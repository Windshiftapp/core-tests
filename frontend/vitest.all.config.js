import fastConfig from './vitest.config.js';

export default {
  ...fastConfig,
  test: {
    ...fastConfig.test,
    include: ['src/**/*.{test,spec}.{js,ts}'],
  },
};
