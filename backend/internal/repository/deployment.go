package repository

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DeploymentStatus string

const (
	StatusPending  DeploymentStatus = "pending"
	StatusCloning  DeploymentStatus = "cloning"
	StatusBuilding DeploymentStatus = "building"
	StatusRunning  DeploymentStatus = "running"
	StatusFailed   DeploymentStatus = "failed"
)

// Deployment holds all metadata for a single deployment.
type Deployment struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Status    DeploymentStatus   `bson:"status" json:"status"`
	SourceDir string             `bson:"source_dir" json:"source_dir"` // absolute path to cloned/extracted source
	SourceURL string             `bson:"source_url,omitempty" json:"source_url,omitempty"`
	Error     string             `bson:"error" json:"error,omitempty"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

type DeploymentRepository interface {
	Create(deployment *Deployment) error
	Update(id string, updateData map[string]any) (*Deployment, error)
	GetByID(id string) (*Deployment, error)
	// Delete(ctx context.Context, id string) error
	// GetAll(ctx context.Context, offset, limit int64) ([]*Deployment, int64, error)
}

type deploymentRepoImpl struct {
	collection *mongo.Collection
}

func NewDeploymentRepository(db *mongo.Database) DeploymentRepository {
	return &deploymentRepoImpl{
		collection: db.Collection("deployments"),
	}
}

func (r *deploymentRepoImpl) Create(deployment *Deployment) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deployment.CreatedAt = time.Now().UTC()
	deployment.UpdatedAt = time.Now().UTC()

	_, err := r.collection.InsertOne(ctx, deployment)

	return err
}

func (r *deploymentRepoImpl) GetByID(id string) (*Deployment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}

	var result Deployment

	err = r.collection.FindOne(ctx, bson.M{"_id": objID}).Decode(&result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (r *deploymentRepoImpl) Update(id string, updateData map[string]any) (*Deployment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}

	filter := bson.M{"_id": objID}
	updateFields := bson.M(updateData)
	updateFields["updated_at"] = time.Now().UTC()

	update := bson.M{
		"$set": updateFields,
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	res := r.collection.FindOneAndUpdate(ctx, filter, update, opts)

	if err := res.Err(); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, errors.New("document not found")
		}

		return nil, err
	}

	var result Deployment

	if err := res.Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
