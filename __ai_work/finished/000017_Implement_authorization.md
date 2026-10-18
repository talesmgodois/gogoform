# 000016 Implement_authorization


You should implement authorization and user access control in this application.

## Backend
- Implement a pkg named auth
- Implement basic authorization using btoa by user and password
- Implement jwt authorization
- Implement signin method to return jwt token
- Add a enum to map the roles (ADMIN, FORM_CREATOR, BASIC) basic is the default one
- Implement swagger updates in order to make the authorization work
- Make some samples of public and authorized routes

## DB
- Update the tables needed, A form can be public or private
- A form can allow a submission to be annonimous or not
- Public available forms, accepts submission anonimous by default(This can be done on the create methods on the golang, but also a checked constraint on the database, it should now allow public_available = true  and accept_annonymous = false)


## Frontend
- Create a authorized page just for testing, but the dashboard and the form builder should not be authorized yet
- The frontend routes definitions should be done in golag, it should be easy to mark a page as public or private(roles based)
- Some forms can be public and some can be private
