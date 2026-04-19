export default {
  extends: ['@commitlint/config-conventional'],
  rules: {
    // Type must be lowercase
    'type-case': [2, 'always', 'lower-case'],
    // Type must be one of the following
    'type-enum': [
      2,
      'always',
      [
        'feat',     // New feature
        'fix',      // Bug fix
        'docs',     // Documentation updates
        'style',    // Code style changes (formatting, etc.)
        'refactor', // Code refactoring
        'perf',     // Performance improvements
        'test',     // Test related changes
        'build',    // Build system or dependency changes
        'ci',       // CI configuration changes
        'chore',    // Other changes that don't modify src or test files
        'revert',   // Revert previous commit
      ],
    ],
    // Subject cannot be empty
    'subject-empty': [2, 'never'],
    // Subject cannot end with a period
    'subject-full-stop': [2, 'never', '.'],
    // Subject must be lowercase
    'subject-case': [2, 'always', 'lower-case'],
    // Subject max length is 72
    'subject-max-length': [2, 'always', 72],
    // Header max length is 100
    'header-max-length': [2, 'always', 100],
    // Body max line length is 100
    'body-max-line-length': [2, 'always', 100],
    // Footer max line length is 100
    'footer-max-line-length': [2, 'always', 100],
  },
};