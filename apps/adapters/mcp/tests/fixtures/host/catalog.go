package host

const AgentCatalog = `schema_version: 1
agents:
  zeta:
    description: Second public package fixture.
    package: github.com/acme/zeta-agent@stable
    visibility: public
  alpha:
    description: First private package fixture.
    package: github.com/private-org/alpha-agent@v1.2.3
    visibility: private
  gamma:
    description: Second private package fixture.
    package: github.com/private-org/gamma-agent@stable
    visibility: private
  beta:
    description: First public package fixture.
    package: github.com/acme/beta-agent@v1.2.3
    visibility: public
`
