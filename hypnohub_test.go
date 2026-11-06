package hypnohub

import (
	"context"
	"fmt"
	"log"
)

func ExampleClient_SearchPosts() {
	client := New()

	result, err := client.SearchPosts(context.TODO(), "dazed comic", 0)
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Println("found", len(result.Posts), "posts")
	// Output: found 100 posts
}

func ExampleClient_SearchTags() {
	client := New()

	result, err := client.SearchTags(context.TODO(), "dazed", 0)
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Println("first tag is", result.Tags[0].Name)
	// Output: first tag is dazed
}
