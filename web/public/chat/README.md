# Generated — do not edit here

`v1.js` is the built script-tag bundle of the chat SDK. It is produced by

    github.com/fadymondy/moharrik-chat-sdk   (npm run build -> dist/chat.js)

and copied here so the app can serve it at /chat/v1.js on its own origin — which is
what makes the browser send the customer's Origin header that the widget key's
allow-list is checked against.

Edit the SDK repo, rebuild, and copy dist/chat.js over v1.js. Editing this file
directly means the next SDK build silently reverts it.
