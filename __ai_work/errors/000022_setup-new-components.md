# 000022 setup-new-components

0. On the home screen, the dashboard should show the total submissions the app has, and how many submissions were made today 


1. You should add the following components

- Check box
- Radio-group
- Image picker - Like file upload, but it should render the image
- Multi option select dropdown
- Rating scale using stars


2. Implement custom components

Every time we customize a component, it should be possible to transform that into a reusable component. Let's say we create a data select with a specific endpoint returning a list of cars. Each reusable component should be stored on a specific table, the field json should be stored on the table
  2.1. The custom components table should have a user_id( the user who created it)
  2.2. The button of the field definition to convert that to a component, should ask if I am sure, with a confirm and cancel button, if saved should appear on the left panel


3. Implement 4 different endpoints on the golang returning fake data, use the __ai_work/_inputs/fake.json, there are 4 collections within the json, create one endpoint for each collection on that json(animes, cars, people and cities) the should allow filtering by any of the fields

4. Insert 8 custom components, 2 for each of the previous endpoints(1 using select data, and the other using autocomplete data) 