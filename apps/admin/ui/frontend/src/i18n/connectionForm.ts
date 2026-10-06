const connectionForm = {
  addTitle: 'Add connection',
  editTitle: 'Edit connection',
  name: 'Connection name',
  url: 'API URL',
  urlHint: 'Use an address reachable from the UI server.',
  username: 'Username',
  password: 'Password',
  credentialsHint: 'Sign-in survives reload. API passwords are kept on the UI server, not in this browser.',
  connect: 'Connect',
  save: 'Save connection',
  testing: 'Testing connection',
  cancel: 'Cancel',
  invalidName: 'Enter a connection name.',
  invalidURL: 'Enter an HTTP or HTTPS URL without embedded credentials, query parameters, or a fragment.',
  invalidUsername: 'Enter a username without colons or control characters.',
  invalidPassword: 'Enter a nonblank password without control characters.',
  connectionFailed: 'Could not test the connection. Please try again.',
} as const;

export default connectionForm;
