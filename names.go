package main

import (
	"fmt"
	"math/rand"
)

var adjectives = []string{
	"Red", "Blue", "Green", "Silent", "Swift", "Calm", "Bold",
	"Bright", "Dark", "Eager", "Fierce", "Gentle", "Happy", "Icy",
	"Jolly", "Kind", "Lazy", "Misty", "Noble", "Odd", "Proud",
	"Quick", "Rare", "Shy", "Tiny", "Urban", "Vivid", "Wild",
	"Zany", "Amber", "Crisp", "Dusty", "Electric", "Fuzzy",
}

var animals = []string{
	"Panda", "Fox", "Hawk", "Wolf", "Bear", "Lynx", "Deer",
	"Crow", "Seal", "Mole", "Frog", "Crab", "Ibis", "Newt",
	"Vole", "Wren", "Yak", "Zebu", "Bison", "Crane", "Dingo",
	"Eagle", "Finch", "Gecko", "Heron", "Iguana", "Jackal",
	"Koala", "Llama", "Moose", "Narwhal", "Otter", "Parrot",
}

func randomName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	animal := animals[rand.Intn(len(animals))]
	return fmt.Sprintf("%s %s", adj, animal)
}
