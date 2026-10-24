using '../templates/ci-bot-identity.bicep'

param bots = [
  {
    envName: 'dev'
    applicationName: '{{ .ci.dev.bot.applicationName }}'
    grantDirectoryRead: true
  }
  {
    envName: 'int'
    applicationName: '{{ .ci.int.bot.applicationName }}'
  }
  {
    envName: 'stg'
    applicationName: '{{ .ci.stg.bot.applicationName }}'
  }
  {
    envName: 'prod'
    applicationName: '{{ .ci.prod.bot.applicationName }}'
  }
]
