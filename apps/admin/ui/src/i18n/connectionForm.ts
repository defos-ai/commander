const connectionForm = {
  addTitle: 'Add connection',
  editTitle: 'Edit connection',
  name: 'Connection name',
  url: 'API URL',
  username: 'Username',
  password: 'Password',
  credentialsHint: 'Credentials stay in memory for this tab and are not saved.',
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
