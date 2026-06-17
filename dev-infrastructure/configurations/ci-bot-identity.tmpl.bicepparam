using '../templates/ci-bot-identity.bicep'

param bots = [
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
