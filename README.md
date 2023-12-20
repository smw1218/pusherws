# Pusher Client

There a several pusher clients (and even more forks) written in Go, but all seem to be incomplete in some way.
Here's another one that's also incomplete :)

This lib was developed against Soketi so it might not be compatible with the hosted Pusher. Maybe someday I'll test pusher too; no promises.

## Features
- Subscribes to Public and Private Channels
- Blocking error handling for Connection and Subscription
- Arbitrary AuthCallback: Clients in other languages allow for any auth method. Since Go clients might be hosted in the cloud, it possible to have access to the secret directly and sign that way rather than a specific HTTP webhook format.
- Channels for Binding: using channels allows for a lot more flexibility in the concurrency model the caller would like to use for processing events.

## TODO
- Replace some of the Client-level channels with something better
- Presence Channels
- Reconnection







