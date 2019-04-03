CREATE TABLE "test" ( 
	"id" Serial NOT NULL,
	"name" Text NOT NULL,
	CONSTRAINT "unique_test_id" UNIQUE("id") 
);