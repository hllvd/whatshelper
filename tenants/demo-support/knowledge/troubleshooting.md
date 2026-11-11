# Guia Rápido de Troubleshooting

- Erro HTTP 401/403 (ERR-403): verifique se o token de API não expirou e possui o escopo `read:reports`.
- Erro HTTP 502 na exportação CSV: exportações acima de 50000 linhas devem usar o endpoint assíncrono `/v2/exports`.
- Configuração de SSO SAML: disponível apenas nos planos Pro e Enterprise em Configurações -> Segurança.
