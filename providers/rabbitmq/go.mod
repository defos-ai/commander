module commander/providers/rabbitmq

go 1.26.7

require (
	commander/framework v0.0.0
	github.com/rabbitmq/amqp091-go v1.15.0
)

replace commander/framework => ../../framework
