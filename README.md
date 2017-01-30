This project attempts to do the following:

1. Creates a new service account
2. Creates a new pubsub topic
3. Grants the service account subscriber permissions on the topic
4. Creates a new JWT key for the service account
5. Authenticates to GCP using the new key
6. Checks permissions on topic
7. Creates a subscription to the topic
8. Grants the service account global pubsub permissions
9. Checks permissions on project
10. Creates a subscription to the topic
11. Cleans everything up so it can be run again

## Known Issues:

Steps 1-5 appear to be successful as you can validate the service account
exists with subscriber permissions to the new topic via the Console UI.
However, Steps 6 and 7 fail and the subscription is not created. Once the
service account is granted global pubsub permissions in Step 8, the
subscription can be successfully created.

1. Without a global role, the subscription cannot be created
2. The method of checking permissions on the topic and project errors with a BadRequest

The former is the real issue. The latter is a side effect of trying to debug.

## Usage:

This code must be run with an owner-level JWT as that is the only role that
allows the creation of Service Accounts.

```
GOOGLE_APPLICATION_CREDENTIALS=/path/to/full/access/jwt.json go run main.go
```

## Results:

```
2017/01/30 14:21:02 Failed to get permissions on topic: googleapi: Error 400: The IAM operation failed with a non-retryable error: Unknown error. See https://cloud.google.com/pubsub/access_control for more information., badRequest
2017/01/30 14:21:02 Failed to create subscription with only a topic role: googleapi: Error 403: User not authorized to perform this action., forbidden
2017/01/30 14:21:04 SUCCESS using global role!
2017/01/30 14:21:04 &{AckDeadlineSeconds:10 Name:projects/my-project/subscriptions/test-sub2 PushConfig:0xc4203c0e60 Topic:projects/my-project/topics/test-topic ServerResponse:{HTTPStatusCode:200 Header:map[Content-Type:[application/json; charset=UTF-8] Server:[ESF] Cache-Control:[private] X-Frame-Options:[SAMEORIGIN] Alt-Svc:[quic=":443"; ma=2592000; v="35,34"] Vary:[Origin X-Origin Referer] Date:[Mon, 30 Jan 2017 21:21:04 GMT] X-Xss-Protection:[1; mode=block] X-Content-Type-Options:[nosniff]]} ForceSendFields:[] NullFields:[]}
```

## Credits:

- https://github.com/GoogleCloudPlatform/google-cloud-go/
- The method of granting permissions to a service account was adapted from
https://github.com/GoogleCloudPlatform/gcp-service-broker/blob/master/brokerapi/brokers/account_managers/service_account_manager.go  
   Copyright the Service Broker Project Authors.
